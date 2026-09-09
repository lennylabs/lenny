# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 4).** Pass 4 read the ledger for the three round-4 non-spec-recheck lenses
(applicability, fresh, test-coverage), the index-and-checklist reconciliation block and the
`[non-spec-recheck.1.fix-G1]` block, and lifted the durable residue every earlier pass had not already
taken. Lifted: fourteen new Settled facts, eleven new Traps (ten of them nearly-filed MISTAKEs recorded
with the reasoning that killed each one, so nobody rebuilds them) and one new Open. Applied one CORRECTS,
against the `bound ⊋ started` trap, which a reader was mis-applying to CODE-2's guard; the trap now carries
the reverse direction as well. Retired nothing new as closed, because round 4 returned three empty findings
lists and closed no standing item. Deleted nothing. Condensed the pass-1 and pass-2 Retired blocks into one
paragraph, keeping the closure reason for each so no later round reopens them; the pass-3 block stands as
written. Did NOT reach 200 lines: this section is roughly 375, because the Retired
condensation paid for most of the twenty-six new entries and the section grew by nine lines net. Eleven
rounds have produced more durable residue than 200 lines holds, and nothing was dropped to reach the number. `### Traps` is where the length lives and it is the
block every lens that reports a USEFUL cites.

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
- **`SlotID == SessionID` is stated nowhere in `spec/`.** §29.10:1457 says only that the gateway mints a `slotId`. SPEC-2's §7.1 block is the first spec statement of the identity; it is code-true and §6.4's `/workspace/slots/{sessionId}/` already implies it, so it is a new claim rather than a contradiction.
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
- **The staged fixture-description sentence overstates by one clause.** `concurrentAdapter` already carries `shutdownErr` AND `shutdownExitedCleanly` with a `Shutdown` handler that reads both (slotbinder_test.go:85-92,:146-155), so non-spec-changes.md:708-714's "injects failures for `StartSession` alone" and "gains ... the `uncleanExit` and `shutdownErr` behaviour" are half-stale. The load-bearing half (per-stage error injection for finalize, setup and credential assignment, the `PrepareWorkspace` and `AssignCredentials` handlers, and request recording) is genuinely absent today. A fixer touching that paragraph should trim the two stale clauses.
- **The files that create an unbound adapter entry call `Shutdown` nowhere.** `coordination_test.go`, `credentials_test.go`, `rotationgate_test.go`, `slotframe_test.go` and `tracingcontext_addressing_test.go` create an unbound entry or call `AssignCredentials` without starting, and `grep -n Shutdown` over them returns nothing, which is the other half of why CODE-1's report-gate move breaks no shipped adapter test.

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
- **MISTAKE, a FOURTH occurrence of the unscoped `leaked` terminal, and the most expensive.** A fix round appended a sentence to §5.2's `**Scrub model.**` paragraph asserting the `leaked` disposition, the held occupancy and the whole-pod replacement trigger with no concurrency qualifier, inside the paragraph the proposal itself declares to hold "on a pod of either concurrency", against staged §7.1's explicit "stated for concurrent occupancy and do not apply there". Five lenses filed it in one round. The structural cause is that the scrub-model paragraph is deliberately concurrency-independent, so ANY sentence appended there that names `leaked`, the trigger or the gauge needs its own scope. It was fixed by splitting the sentence by pod concurrency; check every future append against this.
- **Do NOT collapse that finding into the standing "the `slot_cleanup ──→ leaked` scoping asymmetry is pre-existing" trap.** That trap is about shipped §5.2 and §6.2 prose being loose against the fence. The round-8 defect was a contradiction between two sentences THIS proposal stages, created by a fix and against the proposal's own recorded DECISION. A verifier that merges the two refutes a live finding with an unrelated refutation.
- **Do NOT add a bind-path or a start-state qualifier to §5.2's leak-accounting sentence.** The bind-path qualifier is precisely the carve-out by code path the proposal's own conformance-gap bullet says §5.2's trigger forbids, and it would falsify three untouched shipped sentences (spec/05:545, spec/06:160, docs/reference/state-machines.md:251). The path question is answered in the code lane by the third `accountSlotFailure` caller; the start-state question is answered by CODE-1's `live` gate.
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
- **MISTAKE nearly filed twice: "the per-stage compensation table omits the workspace-prep stage".** True that `materializeSlot`'s first failure branch is `stageWorkspace` (slotbinder.go:284-289, `slotFailureWorkspacePrep`) and that it is the stage producing the class-1 registered-but-unbound residue, but the compensation is stage-agnostic by construction (one wrapper), `FinalizeWorkspace` reaches `ensureSlotPaths` the same way and so produces the identical state, and the adapter half of that residue class is pinned at tier 1 by the "Registered but unbound" case. The upload-free branch is listed separately only because it is the one stage failure that sends no RPC at all.
- **MISTAKE nearly filed: the tier labels on the `podsession` cases.** `podsession/binder_test.go:174-180` starts envtest while the sibling `slotbinder_test.go` cases are filed tier 1, and `slotbinder_test.go` carries `// diagnosis:` on 12 of its 17 tests where the fake-backed `sessionserver/slotretry_test.go` carries none on 15. No gate turns on it, for the reason in the Settled entry on directory-based tiers, and the tests get written and run either way.
- **MISTAKE nearly filed: `export_test.go` against the Testing section's "No export_test.go change".** non-spec-changes.md:639 contradicts :289 and :998 literally, but the sentence's own justification clause scopes it to "no NEW seam is needed for these cases", and an implementor builds S8 from CODE-2's scope, which names the file and the line.
- **MISTAKE nearly filed: the R1b "No impact" impacts row against the proto `Shutdown` comment.** After SPEC-1 the comment at schemas/lenny-adapter.proto:203-206 is further falsified and R1b is the only step allowed to open that file, but the comment is ALREADY stale for any per-slot `Shutdown` on a co-tenanted pod, so this is the same widening-of-pre-existing-looseness class as the §11.4 revoke trap. Checked in the same pass and NOT falsified: the `ReportSessionScrub` comment (:308-311) and `SessionScrubOutcome` (:436-438) both scope themselves to "every session release", which SPEC-3 widens. The correction that IS owed is the standing Deferred against the proto, which R1b carries.
- **MISTAKE nearly filed, and here is what kills it: "the `exited_cleanly` rule's `live ||` disjunct has no test in either direction".** True as stated, since no listed or shipped case drives a `Shutdown` whose `removeSlotTree` fails, but the exclusion paragraph (non-spec-changes.md:695-701) already records that arm as excluded for a portability reason (`removeSlotTree` calls `slotlayout.RemoveTree` with no injectable ops) that covers the started arm identically even though the paragraph names only the unstarted one. The only cheap discriminating assertion left is already enforced by the shipped `TestShutdownIsANoOpForAnAlreadyReleasedSession_spec_4_7` (session_test.go:469-473): inverting the disjunct to `live && treeErr == nil` turns its `ExitedCleanly` assertion red. The residual is one sentence of scope in the exclusion paragraph.
- **MISTAKE nearly filed: the tier-3 dismissal's attribution.** non-spec-changes.md:789-791 says the no-op clean-exit answer is pinned at tier 1 "by the adapter cases above" and end to end at tier 4 below, and neither is literally true, because no listed adapter case and neither tier-4 case drives `Shutdown` for a session the adapter holds NO entry for. The behaviour is covered anyway by the shipped `TestShutdownIsANoOpForAnAlreadyReleasedSession_spec_4_7`, and it is UNCHANGED by CODE-1 (`removed` false gives `closeErr == nil && (false || true)`, the same answer the shipped `bound` gate gives), so the attribution is loose and no changed behaviour is untested.
- **MISTAKE nearly filed: "tier 5 is reached, because CODE-5's reserved and resume callers newly drain a pod nothing drains today".** `DrainSandbox` itself is untouched, the new callers reach an unchanged annotation stamp whose WarmPoolController chain has its own shipped coverage, their own discriminators are asserted at tier 1, and the kube-visible consequence (the per-pod `SandboxClaim` surviving at `bound`) at tier 4. Same shape as the tier-8 item three rounds have declined.
- **Dead end: "withholding `emitFinalUsage` for a bound-but-unstarted entry strands a delegation-tree budget".** The §12.4 budget return runs from the archive path (`pkg/gateway/sessionserver/usage.go:648-668`, `returnTreeBudget`) rather than from the final usage report, and a never-started session's usage meter is empty anyway. Chased as an unlisted consequence of moving `emitFinalUsage` into the `started` block; there is none.

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
- **`terminate` frame `deadlineMs`** — UNVERIFIED, pre-existing and code-lane: the frame requires `deadlineMs` with `minimum: 100`, `RuntimeOps.Terminate` serialises it `omitempty`, and every shipped gateway caller passes 0, so a Full-level runtime receives a frame missing a required field at an ordinary session end. The staged compensation reuses the same call. Whoever owns the tier-3 runtime-ops contract test should confirm. See `spec.2.review-client-surface.1`.
- **`/sessions/` and `/artifacts/` in the widened action list** — UNVERIFIED: `RemoveTree` removes four trees and SPEC-3's widened §5.2 list names two of them plus the timers. The omission predates the proposal, so it was judged incompleteness rather than a defect, but a completeness lens may disagree. See `spec.2.review-citations.1`.
- **"incomplete enumeration" under §29's preamble rule** — OPEN: §29 item 12 gains no fourth `Shutdown` trigger from SPEC-2, and the preamble subordinates a trace only where it DISAGREES. Somebody should decide once whether an incomplete enumeration counts as disagreement and record the answer, because the question returns every round. See `spec.2.review-citations.1`.
- **Concurrent resume leaves a freshly claimed pod at occupancy 1** — OPEN, narrowed: the pod is now counted toward the §5.2 threshold by CODE-5's third `accountSlotFailure` caller and drained, so the hold is bounded by the replacement trigger rather than permanent. Whether `ClaimSlot` pass 1 treats such a pod as slot-bearing was still not verified. See `spec.2.review-performance.1` and `spec-recheck.1.fix-G1.1`.
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
- **The operational lens is no longer swept out** — the entry that said so was written against the spec lane, whose scope excluded the docs edit list and the code-lane metric call sites; reading the non-spec staging produced findings on the first pass. Its advice about where to look (the §16 inventory rows and the `ReportSessionScrub` / `sessions_served` chain) was right and both are now verified clean. See `spec-recheck.1.review-operational.1` and `non-spec-recheck.1.review-operational.1`.
- **Churn from the third accounting caller** — OPEN: on the §7.3 re-attach the accounting is reached for the first time at every concurrency, so at `maxConcurrentSessions: 2` one clean restore failure now drains the freshly claimed replacement pod. The recorded fallback is to account only the leaked arm, stated as a scope in the helper's doc comment. Nobody has priced it against a resume storm. See `spec-recheck.1.fix-G1.1`.
- **Correlated re-attach churn at Tier 3** — OPEN: a whole-node loss puts every slot on those pods into `resume_pending`, and each failed re-attach now drains a fresh replacement pod that never hosted the failed session. The per-instance change is stated in writing; the aggregate against Tier 3's warm-pool headroom is not. A chaos-tier lens is better placed than the performance one. See `non-spec-recheck.1.review-performance.1`.
- **Tier-1 resume accounting fixture** — UNVERIFIED: whether `pkg/gateway/sessionserver`'s existing fakes let a tier-1 case drive `podBinder.Resume` to a failure carrying a `*SlotBindError`. Nobody read `start_test.go`'s resume fixture. See `spec-recheck.1.fix-G1.1`.
- **`compensateFailedSlotBind`'s signature on the resume path** — OPEN: it is staged as `req SlotBindRequest` while `Binder.Resume` holds a `ResumeRequest`. The proposal solved the identical mismatch for `accountSlotFailure` by taking scalars and did not carry it across; `ResumeRequest` does carry `SessionID`, `MaxConcurrentSessions` and `CleanupTimeoutSeconds`, so the scalar remedy works. Five lenses recorded it and none filed it; a fixer must not silently invent a request conversion. See `non-spec-recheck.1.review-applicability.1`.
- **SPEC-3's exclusive-pod arm over-reaches its antecedent** — OPEN: the clause's subject is a bind "abandoned or fails" while §7.1's paragraph binds only "a gateway bind attempt that fails". A READY_TIMEOUT or a revoke retires the pod anyway under spec/05:455; the surviving class is a client terminate at `ready` on a recycling exclusive pool, where the session ends `completed` and the pod is reused. Open decision 11 owns the trigger noun-phrase. See `spec-recheck.3.review-edit-sites.1` and `spec-recheck.3.review-applicability.1`.
- **What disposition the exclusive-pod abandonment arm should name** — UNVERIFIED: §5.2 scrub step 6 marks the scrub failed if `/workspace/slots/` or `/run/lenny/slots/` is non-empty, which looks like the right pointer, but nobody has checked whether a half-completed cleanup's tree trips step 6 or is swept by steps 1-6 first. See `spec-recheck.3.review-applicability.1`.
- **Does §4.6.1 orphan GC reclaim a resume-path leaked slot?** — UNVERIFIED: it drains a `bound` claim whose pod no active session references, so it fires only while the pod carries no other session; once `ClaimSlot` pass 1 places a same-tenant session there the claim is referenced and the leak has no reclaim route. Check `claimOrphanTimeout`'s predicate against a pod at occupancy 1 with one live sibling. See `spec-recheck.1.review-performance.1`.
- **Does any gateway path patch `SandboxClaim.status.phase` to `failed`?** — UNVERIFIED: only a bare DELETE was found on the pre-attached bind-failure path, so the terminal disposition the §4.6.1 projection names may not exist in code. Check the claim-status patch callers before the code lane assumes it. See `spec-recheck.3.review-kubernetes.1`.
- **The reserved-branch defects row contradicts its own header** — OPEN: it sits under `## Defects in the shipped tree that this proposal does not stage` while its own closing sentence says CODE-5 closes the gap, which the deliverable index confirms. One firing adjudicated it `out-of-scope-stands`, the gate refuted that, and the row was left where it stands. Either it leaves the section or the scope call is restated. See `f2.open-decisions.cleanup` and `non-spec-recheck.1.review-citations.1`.
- **Does the resume-path slot failure owe an `error_type` value?** — OPEN: `lenny_slot_failure_total` has no series on that path, so a §7.3 re-attach can move the leak gauge and trip the threshold with no slot-failure signal. A new label value mints no §16 or metrics-reference edit, because both rows state the label without enumerating its values. Either answer is legal and the proposal states neither. See `non-spec-recheck.1.review-operational.1`.
- **Does `releaseCredentials` cover user-source leases?** — UNVERIFIED and disputed, with no correction between the two: `non-spec-recheck.1.review-reliability.1` and `non-spec-recheck.2.review-feasibility.1` read `Credentials.ReleaseSession`'s own doc comment as covering them ("User leases share the lease store the pool assigner uses"), while `non-spec-recheck.2.review-citations.1` reads `releaseCredentials` as touching only `b.Credentials` and never `b.UserCredentials`, so CODE-4's prose overstates it. Pre-existing on either reading and used identically at four shipped sites.
- **CODE-2's rollbacks run on the inbound context** — OPEN: both the `StartSession` and the `Resume` rollback close the runtime under the ctx the gateway's expired deadline cancelled, so `Runtime.Close` may not complete. `context.WithoutCancel` would fix both, as CODE-4's compensation already does. Kept symmetric deliberately so one clause fixes both sites. See `non-spec-recheck.1.fix-G3.1`.
- **The tier-7a park placement, and what the co-tenanted variant asserts** — OPEN: where the park sits relative to `SocketRuntimeProcess.Start`'s `accept` decides which of the two closes tears the process down, and the paragraph deliberately asserts nothing about it. The variant now names the pod-level cohort outcome instead. A later round either names a teardown assertion, which needs the placement settled and a real `SocketRuntimeProcess` behind the gate, or drops the variant. See `non-spec-recheck.3.fix-G1.1` and `non-spec-recheck.3.fix-design-G1.1`.
- **Is tier 8 reached?** — UNVERIFIED and declined by three rounds: the change is a failure and recovery path in the plain sense, but the tier-8 examples in `.claude/rules/test-coverage.md` are all infrastructure failure injection and the adapter-refusal and blob-outage cases are covered at tier 4. See `non-spec-recheck.3.review-test-coverage.1`.
- **`tests/spec-map.json`** — UNVERIFIED: its own header says every PR touching `spec/`, `pkg/`, `schemas/`, `migrations/` or the chart updates it, and sibling proposal 0079 lists it under "Files touched" while 0081 lists it nowhere. `validate-maps` checks only existence, version and dangling paths, so nothing fails. A conformance lens after implementation should decide. See `non-spec-recheck.1.review-edit-sites.1`.
- **`binder_test.go` is in no file list** — OPEN: it holds `fakeAssigner` and the `Binder.Resume` fixtures the staged resume case cites, and appears in neither the gateway-tests list nor "Files touched on application". Same package as `slotbinder_test.go`, so nothing fails to compile. See `non-spec-recheck.3.review-applicability.1`.
- **The Decisions bullet names only two adapter files** — UNVERIFIED: summary.md's decision bullet says the adapter edits land in `session.go` and `runtimegeneration.go`, which the CODE-2 extension made incomplete (`resume.go` takes a rollback and `sdkwarm.go` a call-site change). Its load-bearing half, that `slotsession.go` takes doc comments only and `slot.go` is untouched, still holds. See `non-spec-recheck.2.review-fresh.1`.
- **Does a §15.1 retry reach a pod already stamped for drain?** — UNVERIFIED: nothing in `bindReservedSlot` reads `Status.Phase`, so a retry pinned to `row.PodAssignment` may dial a draining pod, succeed and then die with it. The answer decides whether the withdrawn reserved-branch composite is an accepted limitation or a stuck-session bug. See `spec-recheck.3.review-performance.1`.
- **The `**Recycle lifecycle**` paragraph is in no edit list** — OPEN: spec/05:455 is the surface that governs a one-session pod that does NOT retire, and its only retire rule is the failure-or-crash one, which a `completed` terminate does not trigger. Any future sentence asserting "the pod retires" for an exclusive pod has to survive it. See `spec-recheck.3.review-edit-sites.1`.
- **The tier-7a case names no target file** — OPEN: `## Files touched on application (non-spec)` ends at the directory `tests/tier7a_load_local/`, where every other test surface in that list is a file. Nothing fails on it and it is not marked IMPLEMENTOR'S CHOICE; a later round or the implementor should name the file. See `non-spec-recheck.4.review-test-coverage.1`.

### Deferred

- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S10 (line 24) says "One `accountSlotFailure` helper serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold". That is now incomplete, and the fix that landed in the index-and-checklist reconciliation pass corrected a different half of the same line. What is true: the helper serves every bind path the §7.1 obligation binds — `applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch, and `resumeOnPod`'s `podBinder.Resume` failure branch — so the create-time reserved path AND the §7.3 checkpoint-restore re-attach reach the §5.2 threshold. The helper's request-derived parameters also narrow from `req podsession.SlotBindRequest` to `pool string, maxConcurrentSessions int32`, which changes both existing call sites. Three lenses filed this after the third caller was added.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S9 (line 22) describes CODE-4 as carrying the outcome "as the `leaked` disposition through `ReleaseSlotReservation`". Still true and now incomplete: `Binder.Resume`'s failure branch also returns its error with a `*SlotBindError` in the chain (pod, reserved slot id, stage `resume`, and the compensation's `Leaked`), wrapped inside today's `fmt.Errorf("podsession: resume session on pod %s: %w", ...)` message, which is what carries the re-attach's disposition to S10's third caller. An implementor landing S9 from the checklist alone would leave S10 with nothing to read. Whoever fixes S10 should look at S9 in the same edit.
- DEFERRED [proposals/0081_.../0081_....summary.md, deliverable index CODE-4]: the line reads "releases the session's credential leases" unqualified, for "every post-connection bind failure and at a failed `Resume`". After the fix that moved `b.releaseCredentials` out of `compensateFailedSlotBind` and into the `materializeSlot` wrapper, the resume path releases no lease. The same unqualified wording sits at non-spec-changes.md:25 and :319. All three want the resume carve-out, not just the function body.
- DEFERRED [proposals/0081_.../0081_....summary.md, "Watch out for"]: it reads "`TestValidTransitions_spec_6_2` asserts an exact edge count and fatals on a length mismatch. The §6.2 edit and the `slotstate` edit must land together." Both halves are wrong as an instruction. The test compares a `want` list in `pkg/sandbox/slotstate/slotstate_test.go` against `ValidTransitions()`, both changed by CODE-3 alone, so the spec edit is irrelevant to it; and the checklist puts SPEC-4 in the spec lane at S4 and CODE-3 in the code lane at S6, which the one-lane-per-step rule and the spec write lease make mandatory. What is true instead: `TestValidTransitions_spec_6_2` and `ValidTransitions()` must change in the same step, which is S6, and nothing compares either against spec/06.
- DEFERRED [docs/reference/adapter-contract.md]: line 75's `Shutdown` row states the shipped one-teardown contract, "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`". After SPEC-1 and SPEC-3 that is false in three ways: the slot release now runs for an unbound entry, the runtime teardown and the usage flush run only for a started session, and a pre-start reclaim reports no outcome. The row also needs the no-op clean-exit answer for a session the adapter holds no entry for. Secondary, same cause: `docs/reference/adapter-contract.md:81` ("at each session release ... The gateway increments the pod's served-session count"), `docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68`, and `docs/operator-guide/multi-tenancy.md:72` each say the per-slot cleanup runs "at each session release" and reports its outcome, which SPEC-3 widens (cleanup also runs on an abandoned bind) and narrows (that one reports nothing). The only staged DOCS deliverable is DOCS-1 for `docs/reference/state-machines.md`; `adapter-contract.md` appears in no edit list and the file is named nowhere in the proposal outside this log. CORRECTED: the earlier form of this entry said "tier 11 reconciles them", and half of that is wrong. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only that the row contains "end-of-session teardown", "recycle disposition", "ReportSessionScrub" and "ReportPodScrub", every one of which survives SPEC-1, so the false row passes silently and only a reviewer can catch it. Waiting for the gate would have shipped it. Only the ownership half was right: the non-spec loop owns the edit, and it wants a second DOCS deliverable against `docs/reference/adapter-contract.md`. Scope it to :75. The four secondary sites named above all read "at each session release", which SPEC-3 widens rather than falsifies, so they are NOT edit sites; this reconciles the two standing entries that pulled opposite ways on :81. Eleven separate lenses have now filed the :75 site independently.
- DEFERRED [non-spec-changes / docs, DOCS-1]: `docs/reference/state-machines.md:251` says the `slot_cleanup -> leaked` edge "appl[ies] only to a pod serving more than one concurrent session". DOCS-1 adds only the new `receiving_uploads -> slot_cleanup` row at :234-237; the prose at :251 still scopes the terminal the new edge needs away from a single-session pod. UNVERIFIED and disputed: `non-spec-recheck.1.review-edit-sites.1` judged this entry STALE, on the ground that SPEC-3's concurrency split and staged §7.1 both now scope the `leaked` disposition to a pod serving concurrent sessions and give the exclusive pod §7.1's retirement disposition instead, so the doc prose agrees with the applied spec and the entry should be retired. The index-and-checklist reconciliation pass carried it as still open. Neither corrects the other, so it is kept and flagged rather than dropped; whoever settles it should also settle the standing Open on the §6.2 fence versus §6.2 prose at concurrency 1, which is the same asymmetry.
- DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited") and the `ReportSessionScrub` / `SessionScrubOutcome` comments ("on every session release") are the proto-side statement of the contract §15.4 says is "kept in sync with the prose". SPEC-1's two-teardown split, its no-op clean-exit answer, and SPEC-3's withheld report all falsify parts of them. Programme rule S-2 bars this proposal from opening that file, so the correction belongs to the step that owns it. The `Shutdown` comment is already stale for a co-tenanted pod, so this is a widening rather than a new break. EVIDENCE: schemas/lenny-adapter.proto:203-206,:308-311,:436-438; spec/15:1456.
- DEFERRED [docs/reference/error-catalog.md]: nothing, recorded deliberately. Under the placement-rule design, lines 129, 155 and 156 stay TRUE, because no failure class becomes non-retryable. Recorded explicitly so a later round does not re-file them: they are sites only under the rejected no-retry alternative.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md "Spec files touched"]: the spec/07 half is CLOSED, and the line now reads "the section preamble's premise sentence deleted, a sentence added to step 2, and step 3 replaced". The spec/04 half stands: the list still describes the §4.7 row edit as "(first sentence plus one sentence)" when the replacement swaps two sentences for seven or eight, and the lead-in "Replace the `Shutdown` row's first sentence" is stale in the same way. Six lenses re-derived it and none filed it, because the operative quote-and-replacement pair is exact and no implementor can be misled; one lens argued the parenthetical describes the edit rather than the anchor block and is therefore defensible. A fixer touching the SPEC-1 §4.7 block should correct it in the same edit. A count in that list is a trap that re-arms on every later edit; prefer a named set, as the SPEC-3 entry now uses ("the per-slot cleanup pointer and the cleanup-outcome report rules appended"). The §5.2 entry now also understates: a pod-disposition claim lives in that append and the named set does not hint at it.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :63-66]: "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" has no antecedent for "neither". Round 2 named two placement mechanisms, round 3 deleted the second from the surrounding text, and round 4 did not restore it, so only §5.2's slot retry policy is named. What is true: two mechanisms place a further attempt at the same session, §5.2's slot retry policy and the §7.3 re-attach's whole-pod idle claim through `podclaim.Claimer.Claim`, which cannot select a pod whose occupancy the leaked slot holds; the §15.1 create-time-reserved start is placed by neither. Repair the sentence with that fact rather than by deleting "neither".
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :43-44]: the pointer list ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing") omits the SPEC-2 §7.2 snapshot-close edits entirely, although "Spec files touched" carries them. §7.2 step 3 also restates the reclaim's ordering against the pod release, so the sentence is incomplete rather than wrong. A fixer touching the Design paragraph should fold §7.2 in.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge case "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot"]: the round-4 rewrite says "When the adapter does not acknowledge the reclaim … the adapter's entry survives in whatever state the failed stage left it." That is false for the delivered-but-unanswered case: a `Shutdown` the adapter executed while the response was lost removes the entry, and the gateway still classifies the reclaim as unacknowledged (`err != nil || !cleanly`). What is true instead: an unacknowledged reclaim leaves the gateway unable to tell whether the entry survives, so the retry may find either a surviving entry or none. The later sentences of the same bullet already reason about a lagging reclaim, so the correction is one clause.
- DEFERRED [docs/, four sites that stay TRUE]: `docs/client-guide/session-lifecycle.md:416`, `docs/reference/adapter-contract.md:84`, `docs/runtime-author-guide/index.md:186` and `docs/runtime-author-guide/lifecycle.md:69` also describe the per-slot cleanup and are NOT falsified by SPEC-1 or SPEC-3. Recorded explicitly so the non-spec loop does not widen its docs edit list past the five sites that actually break. Also unchanged: `docs/runtime-author-guide/lifecycle.md:34-43` is a pod-level table in a different vocabulary with no cleanup edge, so SPEC-4 does not reach it.

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
- Seven checklist and summary DEFERREDs (S1, S2, S3, S7, S8, the older S10 on the no-retry rule, and the 0080 §1.19 row), plus the CODE-2 entry and the blob-store edge-case clause. Each was applied: the first seven by the index-and-checklist reconciliation pass, the CODE-2 residue by round 2 taking the second branch and recording both orderings in the accepted-failure-modes list, and the blob-store clause by the edge-case rewrite that states the windowed-counter arithmetic honestly. The newer S9 and S10 corrections that landed after them are carried in Deferred.

## Ledger

### [non-spec-recheck.5.fix-G1.1]

FACT: `concurrentAdapter` (the slot-binder tier-1 fake) already carries three injection knobs, not one: `startErr`, `shutdownExitedCleanly` (defaulting true) and `shutdownErr`, and its `Shutdown` handler reads both shutdown fields and returns either the transport error or `ExitedCleanly: cleanly`. It records no request and declares only NegotiateVersion, FinalizeWorkspace, RunSetup, StartSession and Shutdown. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:71-92,106,122,129,133,146-155.
FACT: `recordingShutdownAdapter` carries per-request recording and `uncleanExit` only. It has no `shutdownErr` field and its handler has no error arm. It implements `Shutdown` alone on top of `UnimplementedAdapterServer`. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:1150-1173.
WATCHOUT: do not add `shutdownErr` or `shutdownExitedCleanly` to `concurrentAdapter` when building S9. They exist. Round 5 corrected the paragraph that told an implementor to add them; the genuinely absent fixture work is per-stage error injection for the finalize, setup, and credential-assignment stages, the `PrepareWorkspace` and `AssignCredentials` handlers, and per-request recording. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:87-92,146-155.
DECISION: corrected the two false clauses in place and kept the inventory sentence — BECAUSE the inventory is what tells the S9 implementor which behaviour already exists, and a corrected sentence costs one line — ALTERNATIVES: deleting the inventory sentence (recreates the ambiguity that produced the finding); re-pointing the paragraph at `recordingShutdownAdapter` as the fake to extend (it serves only `Shutdown`, so the finalize, setup, credential-assignment and start stages would all have to be built from nothing).
FACT: the misstatement was confined to its one paragraph. Neither the CODE-4/CODE-5 checklist steps nor the summary's deliverable list characterises the fixtures' current state, and no other file in the proposal directory mentions either fake, so nothing else needed reconciling. EVIDENCE: grep for `concurrentAdapter|recordingShutdownAdapter|shutdownErr|uncleanExit|shutdownExitedCleanly` across the proposal directory returns only non-spec-changes.md:708-713.


### [non-spec-recheck.5.fix-G2.1]

DECISION: CODE-2's rollback no longer calls `releaseSessionSlot`; it calls `cancelPodMCPIfRuntimeIdle()` directly, on both the `StartSession` and the `Resume` arm — BECAUSE the guard `!ok || st.sessionID != sessionID` fires exactly on "absent OR unbound", so the deregister-and-`removeSlotTree` half can only be a no-op (the reclaim already took entry and tree) or a destruction of a later attempt's entry. It can never be a repair. — ALTERNATIVES: (a) identity-checked release threading the `*slotState` into `deregisterSlotLocked` — rejected, it changes `claimSessionSlot`/`deregisterSlot`/`deregisterSlotLocked` signatures inside a file a later campaign position rewrites, the `Shutdown` handler legitimately wants the id-keyed form, and the same defect stands shipped at five other call sites, so it is its own problem statement; (b) guard the release with a "registry still holds nothing" check — rejected, check and delete are not one critical section and the window spans `Runtime.Close`; (c) drop `Runtime.Close` too — rejected, it leaves the abandoned session resident in the shared runtime while `runtimeLive` does not hold it, so `runtimeIdleLocked` reads true and `soleSession` can name a co-tenant.

FACT: `st.sessionID` has exactly two production writers, `pkg/adapter/slotcreds.go:34` (AssignCredentials) and `pkg/adapter/slotsession.go:87` (claimSessionSlotUnderLock), both set it to the map key and neither ever clears it. So `!ok || st.sessionID != sessionID` is precisely `absent OR registered-but-unbound`. — EVIDENCE: pkg/adapter/slotcreds.go:30-36; pkg/adapter/slotsession.go:74-90.

FACT: `ensureSlotPaths` (pkg/adapter/slot.go:140-148) creates a registry entry with an empty `sessionID` for every workspace-preparation RPC (`staging.go:134,:181,:337`), so the "unbound successor" state a §5.2 retry leaves on the same pod is the ORDINARY state, not a rare interleaving. That is what makes an id-keyed release in the rollback destructive rather than inert. — EVIDENCE: pkg/adapter/slot.go:140-148; pkg/adapter/staging.go:134,181,337.

FACT: `cancelPodMCPIfRuntimeIdle` is already successor-safe. `mcpArmingHeldLocked` reads `s.slots[s.mcpSession]`, so a surface a surviving claimant holds is not cancelled. — EVIDENCE: pkg/adapter/slotsession.go:238-260.

WATCHOUT: the reverse-ordering sub-case does NOT reach the rollback at all. If a successor re-claims through `claimSessionSlot`, `st.sessionID == sessionID` and `noteRuntimeStarted` RETURNS TRUE against the successor's entry, so the abandoned start records into `runtimeLive` and returns success. Any test written as "the rollback runs and the bound successor survives" is wrong on the arm it names. The staged tier-1 case "The rollback destroys no successor" says so explicitly. — EVIDENCE: pkg/adapter/slotsession.go:87-88; non-spec-changes.md, adapter tier-1 case list.

WATCHOUT: `slotTreeProbe` probes the credential FILE (`paths.CredentialsFile`), not the credential directory, so a successor created through `ensureSlotPaths` alone has no credential file and a survival assertion on one there is unsatisfiable. The bound sub-case has to run `AssignCredentials` first. The design's suggested case asserted the credential file in both arms; that was corrected. — EVIDENCE: pkg/adapter/slotsession_test.go:77-85.

DECISION: the §5.2 placement exclusion is carried by pointer — `bindSlotWithRetry` and `applySlotRetryPolicy` take `req *podsession.SlotBindRequest` and `bindConcurrentSlot` passes `&slotReq` — BECAUSE `applySlotRetryPolicy` is not the outermost loop: `runWithQueue` wraps it and `waitInQueue` re-invokes the SAME closure over `bindConcurrentSlot`'s own `slotReq`, so on an `onPoolExhausted: "queue"` pool a value copy loses `ExcludePod` and the retry is re-placed on the pod holding the unacknowledged reclaim. — ALTERNATIVES: (a) return the excluded pod as a third value and re-stamp in the caller — rejected, a third return on a `(result, error)` function with several exits plus a re-stamp per re-entry; (b) scope the guarantee honestly to one invocation and record the queue re-entry as an accepted failure mode — rejected, it leaves the staged NORMATIVE §5.2 sentence (spec-changes.md, `**Max retries:**`) unimplemented by the code the same proposal stages; (c) set the exclusion inside `runWithQueue`/`waitInQueue` — rejected, `runWithQueue` is generic over four call sites and only one binds a slot.

FACT: the queue re-entry path, verified end to end. `bindConcurrentSlot` captures `slotReq` in the `runWithQueue` closure (pkg/gateway/sessionserver/start.go:2606-2609); `applySlotRetryPolicy` takes the request by value (start.go:2807); `ClaimSlot`'s `podclaim.ErrNoConcurrentSlot` is surfaced unwrapped (start.go:2816-2819), classified as exhaustion (queue.go:103-107), routed to `waitInQueue` (queue.go:143-146) and the closure is re-invoked at queue.go:205.

FACT: the pointer change costs no test edits. Every `applySlotRetryPolicy` call site in `slotretry_test.go` and `slotretry_load_test.go` goes through the `req(pool, maxConcurrentSessions)` helper at slotretry_test.go:69, so returning `*podsession.SlotBindRequest` from that helper leaves them all compiling. The load test calls `req(...)` inside each goroutine, so each gets its own pointer and no data race is introduced. `slotBinder.BindSlot` keeps its by-value parameter (`binder.BindSlot(ctx, *req)`), so `Binder`, `fakeSlotBinder` (slotretry_test.go:25) and `concurrentSlotBinder` (slotretry_load_test.go:33) are untouched. — EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:69,:81-421; slotretry_load_test.go:84.

FACT: `slotretry_test.go` and `queue_internal_test.go` are both `package sessionserver` (internal), so the new tier-1 case can compose `runWithQueue` with the unexported `newPodClaimQueue` and its injected clock. — EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:3; queue_internal_test.go:3,:94-95.

CORRECTS [non-spec-changes.md `**When it does not fire.**` bullet, as it stood before this round]: it named the reclaim's lagging `os.RemoveAll` as the producer on the branch where `ExcludePod` does not fire. It cannot be: that branch is an ACKNOWLEDGED reclaim, and the `Shutdown` handler completes `removeSlotTree` before it builds the response (pkg/adapter/session.go:238-282). The actual producers there are the abandoned attempt's own in-flight rollbacks on the same pod: the shipped pre-`Runtime.Start` `releaseSessionSlot` calls (pkg/adapter/session.go:133,:147,:157), which are a pre-existing hazard, and CODE-2's rollback `Runtime.Close`.

DEFERRED [pkg/adapter/session.go, pkg/adapter/resume.go]: the shipped pre-`Runtime.Start` failure branches release the slot by session identifier alone (`session.go:133,:147,:157`; `resume.go:69,:73,:89,:107,:126,:134,:141`). Under `SlotID == SessionID` a lagging one deletes a later attempt's entry and `RemoveAll`s the tree, uploads and credential directory that attempt staged, exactly as the rollback would have. The claim that "`releaseSessionSlot` is correct against an entry a concurrent reclaim removed" is false for all of them. This proposal does not open those branches; the correction is recorded in the `**When it does not fire.**` bullet and owed to a later proposal.

WATCHOUT: do NOT weaken summary.md's `ExcludePod` sentences (the 0080 §1.19 impact row's placement clause, and the CODE-5 line in the Summary's traps list). The queue-re-entry finding falsified them against the OLD design; the pointer fix makes them true as written. The symmetric edit — weakening both clauses because both findings touched the same table cell — would leave the proposal under-claiming a guarantee its own staged code provides.

UNVERIFIED: the "What does not change" enumeration in non-spec-changes.md lists the in-process field, error field, threaded parameter and predicate split, and does not mention that two internal gateway helpers now take the bind request by pointer. Deliberately not added: the paragraph's own claim (no new RPC, frame, wire field, flag, metric, or operator-tunable) stays true, and the standing context records five bookkeeping candidates of this class already refuted on the materiality bar. A later round that disagrees should add one clause rather than restructure the paragraph.


### [non-spec-recheck.5.fix-design-G1.1]
DECISION: Apply the reviewer's suggested rewrite verbatim to the two clauses of the `Fixture work is part of the deliverable` paragraph (non-spec-changes.md:708-714); nothing else in the paragraph, no staged code, no staged spec text, no test case changes — BECAUSE both halves of the attribution are false against the tree and the correction is confined to one sentence pair — ALTERNATIVES: (a) delete the fixture-inventory sentence and keep only the "it gains X" list — rejected, the inventory is what tells S9's implementor which behaviour is already there and dropping it re-creates the ambiguity that produced this finding; (b) re-point the paragraph at `recordingShutdownAdapter` as the fake to extend — rejected, it implements only `Shutdown` (binder_test.go:1167) while the table needs the finalize/setup/credential/start stages `concurrentAdapter` already serves.
FACT: `concurrentAdapter` (pkg/gateway/podlifecycle/podsession/slotbinder_test.go:71-155) already declares `startErr`, `shutdownExitedCleanly` and `shutdownErr`, and its `Shutdown` handler at :146-155 reads the latter two; it records no request. It serves NegotiateVersion, FinalizeWorkspace, RunSetup, StartSession and Shutdown only — no `PrepareWorkspace`, no `AssignCredentials`. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:84-92,:106-155
FACT: `recordingShutdownAdapter` carries `reqs` (per-request recording) and `uncleanExit` only; it has NO `shutdownErr` field and its handler has no error arm. It lives in binder_test.go but the same package, so `slotbinder_test.go` can borrow its recording pattern directly (already referenced there at :627,:699). EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:1156-1173
WATCHOUT: the genuinely-absent list is narrower than the paragraph implies — per-stage injection for finalize/setup/credential-assignment, the two missing handlers, and request recording. Do not "add `shutdownErr` and `shutdownExitedCleanly` to `concurrentAdapter`": they are already there and a fixer following the uncorrected paragraph will duplicate fields. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:87-92
FACT: file:line citations into `_test.go` are the established convention in this proposal's non-spec-changes.md (46 `.go:<line>` citations), so the replacement clause's `binder_test.go:1156-1173` citation matches the file's own style rather than introducing one. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md:289-298,:758


### [non-spec-recheck.5.fix-design-G2.1]

DECISION: CODE-2's rollback drops `s.releaseSessionSlot(sessionID)` and calls
`s.cancelPodMCPIfRuntimeIdle()` directly, on BOTH the `StartSession` and the `Resume` arm —
BECAUSE the guard that fires the rollback has already established that the registry holds no
entry bound to this session, so the deregister-and-`RemoveAll` half can only ever destroy
somebody else's entry, never this attempt's. `cancelPodMCPIfRuntimeIdle` is the only half the
rollback wants there, and it is already successor-safe through `mcpArmingHeldLocked`
(pkg/adapter/slotsession.go:238-260). — ALTERNATIVES: (a) make the release identity-checked by
threading the `*slotState` the claim returned into `deregisterSlotLocked` and deleting only
when `s.slots[id] == st` — rejected for this proposal: it fixes a class that is PRE-EXISTING
(see the MISTAKE/FACT entries below), it changes `claimSessionSlot`'s and `deregisterSlot`'s
signatures inside `pkg/adapter/slotsession.go`, a file a later campaign position rewrites, and
the `Shutdown` handler legitimately wants the id-keyed form, so two forms would have to
coexist. (b) leave the call and add a same-identity precondition in prose — rejected, prose
cannot hold a race. (c) widen `ExcludePod` to fire on every failed bind so no retry ever
returns to the pod — rejected, it contradicts §5.2's own preference for the same pod, costs
availability on the ordinary transient retry, and needs its own spec sentence.

DECISION: CODE-5 carries `ExcludePod` by making `bindSlotWithRetry` and `applySlotRetryPolicy`
take `req *podsession.SlotBindRequest`, with `bindConcurrentSlot`'s `runWithQueue` closure
passing `&slotReq` — BECAUSE the exclusion's intended lifetime is "this client request's
remaining bind attempts" and the value that lives exactly that long is `bindConcurrentSlot`'s
own `slotReq`, which the queue closure re-passes on every re-entry. `binder.BindSlot(ctx, *req)`
keeps the `slotBinder` interface and every fake unchanged, and `slotretry_test.go`'s `req(pool,
n)` helper returning a pointer keeps all twelve existing call sites compiling. — ALTERNATIVES:
(a) have `applySlotRetryPolicy` return the excluded pod as a third value — rejected, a third
return on a `(result, error)` function that returns early at five points. (b) scope the
exclusion to one `applySlotRetryPolicy` invocation and record the `queue`-pool re-entry as an
accepted failure mode — rejected, it would leave the STAGED, NORMATIVE §5.2 sentence at
spec-changes.md:395 unimplemented by the staged code, which is worse than a signature change.

FACT: the guard CODE-2 stages (`!ok || st.sessionID != sessionID`) does NOT fire when a
successor re-claimed the same session id, because `SlotID == SessionID` and both binders set
`st.sessionID` to the map key. It fires only for an ABSENT entry or an UNBOUND one. The unbound
case is the reachable destruction: `ensureSlotPaths` (pkg/adapter/slot.go:140-148) creates an
entry with an empty `sessionID` for every `PrepareWorkspace`/`FinalizeWorkspace`/`RunSetup`, so
a retry's workspace staging creates exactly the entry a lagging rollback's `releaseSessionSlot`
would delete, `RemoveAll`-ing the uploads it just staged. EVIDENCE: pkg/adapter/slot.go:105-148;
pkg/adapter/slotsession.go:87; pkg/adapter/slotcreds.go:32-36.

FACT: `st.sessionID` has exactly two production writers and NEITHER ever clears it
(pkg/adapter/slotcreds.go:34, pkg/adapter/slotsession.go:87). An entry once bound stays bound
until it is deleted, so `st.sessionID != sessionID` is equivalent to `st.sessionID == ""` at
every call site keyed on the session id.

FACT: the compensating `Shutdown` completes `removeSlotTree(st)` BEFORE it answers
(pkg/adapter/session.go:238-282), so on the branch where `ExcludePod` does NOT fire (the
compensation was acknowledged and the reservation released cleanly) the reclaim's `os.RemoveAll`
cannot be the lagging producer. non-spec-changes.md's "When it does not fire" bullet attributes
it there anyway. The real producers on that branch are the abandoned attempt's own in-flight
rollbacks: the SHIPPED `releaseSessionSlot` calls at pkg/adapter/session.go:133,147,157 (all
before `Runtime.Start` returns) and CODE-2's rollback `Runtime.Close`.

MISTAKE (pre-existing, file as its own finding — do NOT fix in this round): every shipped
`releaseSessionSlot` call on a start-path failure branch is keyed by session id and snapshots
nothing (pkg/adapter/session.go:133,147,157; pkg/adapter/resume.go:134,141), so any one of them
that lags past a reclaim plus a re-claim deletes a successor's entry and `RemoveAll`s its tree.
CODE-2 was about to add a fifth instance of that pattern on the widest window of the five. This
round removes the new instance only. The class fix is the identity-checked deregister in
alternative (a) above and wants its own problem statement.

WATCHOUT: `applySlotRetryPolicy` is NOT the outermost loop on the concurrent bind path.
`bindConcurrentSlot` wraps `bindSlotWithRetry` in `runWithQueue`, and on an
`onPoolExhausted: "queue"` pool `waitInQueue` re-invokes the SAME closure with the SAME captured
`slotReq`. Any state a retry policy leaves on its own value copy of the request is discarded at
that boundary. The standing-context entry "`applySlotRetryPolicy` takes `req` by value, so
setting `req.ExcludePod` on the retry iteration is scoped to that request's remaining attempts"
is true of the invocation and FALSE of the request. EVIDENCE: pkg/gateway/sessionserver/start.go:2606-2609,
:2735-2736, :2807; pkg/gateway/sessionserver/queue.go:134-146, :205.

CORRECTS [Settled: "`applySlotRetryPolicy` takes `req podsession.SlotBindRequest` by value, so
setting `req.ExcludePod` on the retry iteration is scoped to that request's remaining attempts"]:
"that request" is the value copy, not the client request. A `queue`-pool re-entry rebuilds the
copy from `bindConcurrentSlot`'s untouched `slotReq` and the exclusion is gone. The Settled
entry "Each request builds a fresh `SlotBindRequest`, so `ExcludePod` never crosses a request
boundary" is right about the boundary and was read as also meaning the exclusion survives to it,
which it does not until the pointer change lands.

WATCHOUT: `summary.md:441` is ONE table cell answering the problem statement's own instruction
about proposal 0080 §1.19, and two independent mechanisms write into it. Only its rollback
clause changes this round. Its `ExcludePod` clause ("a retry the §5.2 slot retry policy places
issues no fence to a pod that may still hold the prior attempt's entry") is made TRUE by the
pointer fix rather than falsified by it, so weakening it is the wrong edit. Same for
summary.md:127.

UNVERIFIED: on a same-pod retry after an ACKNOWLEDGED reclaim, the pod MCP surface armed by the
abandoned attempt can survive (`mcpArmingHeldLocked` is true whenever any entry stands under
the id, including the retry's unbound workspace-prep entry), and the retry's own
`claimPodMCPStartLocked` then returns `startMCP == false` because `s.mcpSession == sessionID`,
so the retry inherits a surface serving the abandoned attempt's manifest nonce. This looks
pre-existing rather than introduced here, and it is outside both findings. Somebody should
check whether the nonce mismatch is real and, if so, file it. EVIDENCE:
pkg/adapter/slotsession.go:254-260, :288-292 (`claimPodMCPStartLocked`).

OPEN: `spec-changes.md:395`'s staged §5.2 sentence is normative and, with the pointer fix, is
implemented for every retry the policy places within one client request. It is still NOT
implemented for a SECOND client request (a client-driven retry of `/start`), which the proposal
already records as an accepted failure mode. Nobody has asked whether §5.2's sentence should
say so explicitly; it currently reads unqualified.


### [non-spec-recheck.5.review-applicability.1]

DECISION: filed exactly one finding, the stale `concurrentAdapter` / `recordingShutdownAdapter`
fixture attribution at non-spec-changes.md:708-714 — BECAUSE it is a verified false statement
about the tree inside text the proposal itself calls "part of the deliverable rather than an
assumption", it has sat unfixed since round 4 as Settled 184 with nobody filing it, and its fix
lands wholly in the non-spec staging. ALTERNATIVES: leaving it as a Settled note again (rejected:
four rounds of "a fixer should trim it" produced no fixer); filing the tier-4 second-Sandbox,
the CODE-3 same-step sentence, the CODE-4 `SlotBindRequest`-on-resume signature, or the
adapter-contract.md:75 docs site (all already refuted or standing DEFERRED — do not refile).

FACT: `s.podBinder` in `pkg/gateway/sessionserver` is a CONCRETE `*podsession.Binder`, not an
interface, so `resumeOnPod`'s `podBinder.Resume` has no fake seam the way `applySlotRetryPolicy`
has `slotBinder`. The staged tier-1 "resume path" case's accounting half ("reaches `MarkLeaked`
… through `resumeOnPod`'s accounting call") is still buildable, because `Binder.DialAdapter` is
an injectable field and several tests already build `&podsession.Binder{Client: c, Namespace: ns}`
against a fake k8s client, but it is a real fixture build rather than a fake-swap. This closes
the standing Open "Tier-1 resume accounting fixture" on the seam question and leaves only the
cost question. EVIDENCE: pkg/gateway/sessionserver/sessionserver.go:188;
pkg/gateway/sessionserver/start_preclaim_internal_test.go:655,:720,:779;
pkg/gateway/sessionserver/start.go:2727 (the `slotBinder` seam that exists only for the retry path).

FACT: the checklist re-derives clean as an execution sequence, again, and so does the gate state.
Ten steps, ten deliverables, one lane each (spec ×4 leading, docs ×1, code ×5), no deliverable
named twice, every `Depends on` naming an earlier step, no checked box. Every code target named in
the non-spec staging exists with the stated signature, including the ten `noteRuntimeStarted` call
sites, the five `cl.Close()` calls in `materializeSlot`, the five production `ReleaseSlotReservation`
sites plus the interface and two fakes, `expiredByUptime`'s two candidate passes, `match.Pool` and
`maxConcurrentSessions()` in `resumeOnPod`'s scope, and `slotstate`'s `ReceivingUploads`/`SlotCleanup`
constants. `cl.Shutdown`'s fourth parameter is a `time.Duration`, so the staged literal `0` compiles.
Do not re-derive any of this. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807;
pkg/gateway/podlifecycle/podsession/slotbinder.go:265,:286-321,:493; pkg/sandbox/slotstate/slotstate.go:60-113.

FACT: the staged tier-11 edit passes on both loops after SPEC-4 and DOCS-1. `requireLine` returns
the FIRST matching line and does not require uniqueness, so neither SPEC-4's new fence edge nor its
new prose paragraph can break `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency`, and the
new `generalSlotEdges` entry `"receiving_uploads ──→ slot_cleanup"` is absent from the scoped block
so the negative loop stays green. The DOCS-1 row spells `` `receiving_uploads` | `slot_cleanup` ``
verbatim, matching the proposed `requireAllContain` substring. EVIDENCE:
tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:158-165;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55,:70;
docs/reference/state-machines.md:234-237.

WATCHOUT: the r5 snapshot (`scratchpad/cp-snap/0081/non-spec-recheck-r5`) is byte-identical to the
live staging, and so is `-r4` and `-r4-start`. The last real staging delta is r3→r4, the tier-7a
paragraph rewrite at non-spec-changes.md:829-872. The orchestrator brief's "this lane's staging
changed after its last review converged" was not observable for the third round running. Diff
against `non-spec-recheck-r3` (or older) rather than the snapshot the brief names, and do not spend
a round hunting a fix-stage delta. EVIDENCE: `diff -rq -x '*review-log*'` over
scratchpad/cp-snap/0081/non-spec-recheck-r4 and -r5 returns nothing.

FACT: the rewritten tier-7a paragraph's citations all hold. `gatedRuntime` is at
tests/tier7a_load_local/podmcp_arming_handoff_test.go:46-101 and its `Close` also parks, but on a
separate close gate, so an unarmed session's `Close` returns immediately and the compensating
`Shutdown` does not deadlock against the parked `Start`. The two-RPC rendezvous precedent is at
podmcp_once_per_pod_start_race_test.go:236-256 and already drives `Resume` with a checkpoint id and
no chunks, which is exactly the arm the paragraph specifies. `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
covers both the sibling-active early return and the last close at pkg/adapter/socketruntime_test.go:252-300.
The one loose phrase, "`noteRuntimeStarted` is the only writer of `runtimeLive`", is about insertion
only (`noteRuntimeClosed` deletes), and the same paragraph says so two sentences earlier; three
lenses' worth of value is not in re-deriving it.


### [non-spec-recheck.5.review-citations.1]

FACT: The whole staging is byte-identical to `scratchpad/cp-snap/0081/non-spec-recheck-r4`, `-r4-start`, `-r5` and `-r5-start`. The last real staging delta was r3 → r4 (the tier-7a paragraph, non-spec-changes.md:829-872). `diff -rq` the snapshot directory before hunting a delta; the orchestrator's "the staging changed" line is not observable. — EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r3 vs the live proposal (only non-spec-changes.md differs)

FACT: The citation sweep over the non-spec staging and the summary is now COMPLETE and clean apart from one item. I opened and confirmed, line by line: scrubreport_server.go:451-481; session.go:156,:163,:243,:259-261,:267,:309-321; slotsession.go:75-110,:174-189,:214-220,:238-260,:338,:375,:398-408; slotcreds.go:23-52; runtimegeneration.go:26-49,:58-69,:83-88; resume.go:42,:50,:101-104,:134,:140,:141,:144; sdkwarm.go:217,:261,:297; holdstate.go:89-99,:177-190,:251; adapterevents.go:100-108,:153-155; embedded.go:188-204; mcpruntime.go:266-291; socketruntime.go:156-161,:181-202,:184,:220,:373-378,:398-417,:427-429,:435-467,:441-446; export_test.go:45; usage_test.go:233,:350-363; adapterevents_test.go:95,:104-106,:184; podmcp_arming_internal_test.go:84,:185,:230; slotsession_test.go:210,:308-339; socketruntime_test.go:252; slotbinder.go:133,:172,:210-224,:217-220,:230-254,:254,:265,:286/:293/:301/:308/:321 (the five closes),:415-478,:449-473,:487-504,:528-545,:542-543; binder.go:841,:843-965,:957,:1009,:1097-1106,:1263,:1590-1631,:1674-1704,:1710-1717; binder_test.go:301,:322,:1150-1173; slotfailure.go:74; start.go:2594-2605,:2725-2729,:2807-2885,:2834-2848,:3241-3255,:3648-3682,:4029,:4041-4043; slotclaimer.go:830-836,:845-847,:850-878,:881-885; slotclaimer_test.go:648,:682; pgstore.go:732-744; migrations/0080_...up.sql:19-25; scrubreport_server.go:98-110; adapterclient/client.go:464,:807-824; user_revocation.go:45,:129; main.go:350-359; slotstate.go:70-114,:105-114; slotstate_test.go:12-44; registry.go:8-12,:99-116; tier11_docs test :32-37,:55,:64,:70,:102-108; tier7a podmcp_arming_handoff_test.go:43-101, podmcp_once_per_pod_start_race_test.go:239-256, shutdown_drain_gate_race_test.go:209-218,:232,:316,:438-473; tier4 concurrent_workspace_test.go:96,:109,:119,:307; docs/reference/state-machines.md:228-237,:251; spec/04:151-155,:157,:686,:854; spec/05:453,:545,:549,:551,:553,:555-557; spec/06:140-160; spec/07 anchors; spec/29:586-591,:669-679,:693-702,:704-711; spec/28:1082; schemas/runtime-ops-events.schema.json:181; gateway-runtime-comms-remediation.md:1268-1274,:1883; commit f37e867b8; 0075 spec-changes.md:112-119. Every one resolves. Do NOT re-run this sweep.

FACT: `spec/05_runtime-registry-and-pool-model.md:551` really does write the index predicate as `sessions(pod_assignment) WHERE state = 'active'`, while `migrations/0080_sessions_active_by_pod_index.up.sql:19-21` QUOTES the spec as `sessions(pod_name) WHERE state = 'active'`. The summary's shipped-defect row quotes the spec correctly; the migration's quotation is the stale one. A later reader comparing the two will think the proposal misquoted. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:551; migrations/0080_sessions_active_by_pod_index.up.sql:19-21

MISTAKE: non-spec-changes.md:708-714 (the gateway-tests fixture paragraph) misattributes two shipped fixture fields in both directions. `concurrentAdapter` already carries `shutdownErr` (slotbinder_test.go:89-92) and `shutdownExitedCleanly` (:84-88), both read by its own `Shutdown` handler (:146-155), so "injects failures for `StartSession` alone" is false and `shutdownErr` is not something it "gains"; and `recordingShutdownAdapter` (binder_test.go:1156-1173) carries request recording and `uncleanExit` but NOT `shutdownErr`, so sourcing `shutdownErr` from it names the wrong fixture. The load-bearing half (per-stage injection for finalize/setup/credential-assignment, `PrepareWorkspace` and `AssignCredentials` handlers, request recording) is genuinely absent and stands. FILED this round. The review log carried it as a Settled note ("overstates by one clause") for several rounds and nobody filed it; it is two clauses, not one.

WATCHOUT: `pkg/gateway/podlifecycle/podsession/binder_test.go` holds BOTH `fakeAssigner` (:296-324) and `recordingShutdownAdapter` (:1156-1173), and the tier-1 gateway `Files:` line (non-spec-changes.md:705-706) names neither it nor the file. Same package as `slotbinder_test.go`, so nothing fails to compile. Already an OPEN; do not re-derive.

WATCHOUT: non-spec-changes.md:530-534 cites "(`pkg/gateway/podlifecycle/podsession/binder.go`, `releaseResumeSlot`'s own no-op guard)" as evidence that `reserveResumeSlot` returns an empty slot id when `MaxConcurrentSessions <= 1`. The stated fact is proven at `reserveResumeSlot` binder.go:1675-1677, not at `releaseResumeSlot`'s `if slotID == ""` guard (:1711-1713). I did NOT file it: `releaseResumeSlot`'s doc comment (:1706-1709) does say "It is a no-op on an exclusive pool, which reserved nothing", so the pointer supports the sentence's conclusion even though it names a different function. Same class as the standing "citation ranges to normalise, not to file" trap.

WATCHOUT: the summary's Decisions "File-collision discipline" bullet (summary.md:88-92) says "the adapter edits land in `session.go` and `runtimegeneration.go`", which is incomplete after the CODE-2 extension: `resume.go` takes a rollback and `sdkwarm.go` a call-site change, both named in the CODE-2 deliverable index and in "Files touched on application". I did NOT file it: the bullet's purpose is the later campaign position's collision with `slotsession.go` and `slot.go`, and that half ("doc comments only" / "not at all") is still true, so the collision analysis is unaffected. It is the same bookkeeping class this loop has refuted five times. Standing OPEN.

FACT: `noteRuntimeStarted` has exactly ten call lines and no eleventh: three production (session.go:163, resume.go:144, sdkwarm.go:261) and seven test (export_test.go:45, usage_test.go:233, podmcp_arming_internal_test.go:84/:185/:230, adapterevents_test.go:95/:184). CODE-2's enumeration matches the tree exactly, including which of them already hold a bound entry when they record: `claimSessionForTest`/`ClaimSessionForTest` and `bindSessionForTest` both set `st.sessionID`, and `claimSessionSlot("alice", ...)` does too, so only the two `adapterevents_test.go` sites need the new `bindSessionForTest` call. — EVIDENCE: `grep -rn noteRuntimeStarted pkg/ cmd/ tests/`; pkg/adapter/export_test.go:40-46; pkg/adapter/usage_test.go:350-363; pkg/adapter/slotsession.go:87-88

USEFUL [Settled: "Every code target in the non-spec staging exists with the stated signature"]: accurate and saved a full re-derivation of the `materializeSlot` / `ReleaseSlotReservation` / `connectSlot` surface. The five `ReleaseSlotReservation` production sites are exactly start.go:2834 (applySlotRetryPolicy), start.go:3246 (rollbackClaim), slotbinder.go:172 (ClaimSlot connect-stage), slotbinder.go:217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), plus the interface at start.go:2727 and the two fakes at slotretry_test.go:52 and slotretry_load_test.go:33. CODE-4's six-row call-site table maps onto them one for one.

USEFUL [Traps: "Dead end, and here is its resolution: `docs/reference/adapter-contract.md:75` is the site and `:81` is not"]: kept me from re-filing the docs-mirror gap. It is a DEFERRED, not a live finding.


### [non-spec-recheck.5.review-client-surface.1]

DECISION: returned an EMPTY findings list — BECAUSE every externally-consumed contract this proposal
touches was traced to all of its parallel representations and each is either staged, unchanged, or
already adjudicated. ALTERNATIVES considered and rejected: (a) `docs/reference/adapter-contract.md:75`
— on the orchestrator's refuted list, filed by eleven prior lenses and refuted by the material
skeptic; (b) the `schemas/lenny-adapter.proto` `Shutdown`/`ReportSessionScrub`/`SessionScrubOutcome`
comments — barred by programme rule S-2 and already recorded as a standing DEFERRED that names R1b;
(c) the summary's R1b impacts row reading "No impact / Nothing" while the proto comments go further
stale — Traps 283 records a prior lens nearly filing this and declining it, and five bookkeeping-class
findings have already been refuted on the materiality bar; (d) `docs/reference/state-machines.md:251`
— consistent with the applied spec, since staged §7.1 and SPEC-3 both scope `leaked` to concurrent
occupancy; (e) "Testing reaches no tier 10" — the tier-9 analogue was already refuted and tier 10's
adapter cases all drive started sessions.

FACT (delta): the non-spec staging is BYTE-IDENTICAL between the r4 and r5 snapshots.
`diff -rq scratchpad/cp-snap/0081/non-spec-recheck-r4 scratchpad/cp-snap/0081/non-spec-recheck-r5`
reports only the two review-log files. There was no fix round between r4 (three empty lists) and r5,
so "read the delta first" has no delta to read this round; the whole staging is the scope.
EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r4 vs .../non-spec-recheck-r5.

FACT: CODE-1's `ExitedCleanly` rewrite breaks NO shipped tier-3 or tier-10 wire assertion, and I
verified this rather than assuming it. Every case that asserts "the outcome and the flag must agree"
drives a full `StartSession` first, so `live` is true and the expression collapses to today's
`closeErr == nil`. Sites checked: tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:135-140
(`startAndShutdownSlot` runs `StartSession`), :285, :318; tests/tier10_conformance/
recycle_scrub_conformance_test.go:175-197 and :393. For a session the adapter holds no entry for,
`removed` is false so the answer is `true && (false || true)` = true, identical to the shipped
`bound` gate. Do not re-derive this.

FACT: the compensation's `terminate`-frame claims all resolve. `drainReason`'s default arm is at
pkg/adapter/session.go:309-321 (func at :314), the enum is spec/28_communication-channels.md:1082 and
schemas/runtime-ops-events.schema.json:181, and `ShutdownRequest.reason` is a bare `string` with no
enum (schemas/lenny-adapter.proto:1611). The `bound` gate the proposal cites at session.go:243 and
the drain at :259-261 are exactly where it says. `"slot_bind_failed"` mints no wire value.

FACT: `slotCleanupBudget`'s input is real on every concurrent pool, contrary to what
`SlotBindRequest.CleanupTimeoutSeconds`'s own doc comment suggests. The comment at
pkg/gateway/podlifecycle/podsession/slotbinder.go:97-108 says the field is "Empty/zero on a
non-recycling concurrent pool", but `slotBindRequest` sets it unconditionally from
`match.CleanupTimeoutSeconds` (pkg/gateway/sessionserver/start.go:2567), which comes from
`sessionPolicy.cleanupTimeoutSeconds` rather than from the recycle block
(pkg/gateway/sessionserver/sessionserver.go:2543; pkg/gateway/podlifecycle/podsession/resolve.go:195-201).
So the budget is the §5.2 figure and not the 5s floor on a non-recycling concurrent pool. A future
round tempted to file "the budget silently collapses to 5s" should read start.go:2567 first; the loose
comment is pre-existing and is not 0081's.

FACT: `cl.Shutdown`'s fourth parameter is a `time.Duration`, not an int
(pkg/gateway/runtime/adapterclient/client.go:807), so the staged `cl.Shutdown(rctx, req.SessionID,
"slot_bind_failed", 0)` compiles. Its doc comment's idempotence sentence (":803-804", "a request
naming a session the adapter has already released is a no-op reporting a clean exit") stays TRUE
under CODE-1 and is not an edit site.

FACT: `docs/api/internal.md:95-160` describes the adapter RPCs under RETIRED names and a retired field
set (`StopSession`/`StopSessionRequest`/`StopSessionResponse.clean_exit`, and a
`StartSessionResponse{success, error_code, error_message}` where the shipped proto has
`refusal_reason` at schemas/lenny-adapter.proto:958-962). That whole block predates this proposal and
is not an edit site for it. A client-surface lens that greps for `Shutdown` mirrors will land here;
do not file it.

FACT: the four docs sites the standing DEFERRED calls "secondary" really do all read "at each session
release" and stay true under SPEC-3's widening. Read cold this round:
docs/reference/adapter-contract.md:81, docs/reference/execution-modes.md:68,
docs/operator-guide/multi-tenancy.md:72, docs/operator-guide/security-principles.md:33. This
independently confirms the Traps entry that resolves the ":75 is the site, :81 is not" disagreement.

FACT: the per-slot vocabulary has no SDK, CRD, or OpenAPI mirror.
`grep -rln "slot_cleanup|receiving_uploads|ReportSessionScrub|exited_cleanly|session release" sdks/`
returns nothing; `concurrent_slots_exhausted` and `WARM_POOL_EXHAUSTED` do not appear in
pkg/gateway/externalapi/openapi/openapi.json (note: the openapi document is at
`pkg/gateway/externalapi/openapi/openapi.json`, NOT the `pkg/gateway/openapi/` path the lens brief
gives); `cleanupTimeoutSeconds` appears in no file under `charts/lenny/crds/` or `pkg/apis/`, only as
an untyped integer in the admin OpenAPI block at openapi.json:403. DOCS-1's edge vocabulary appears in
docs/ only at docs/reference/state-machines.md:234-237 and :251; the `receiving_uploads` hits at
:149-151, :175-176 and in docs/runtime-author-guide/lifecycle.md:22-52 are the POD-level machine in a
different vocabulary and are not edit sites.

FACT: no tier-11 gate other than the one DOCS-1 stages reads the general per-slot table.
`concurrent_slot_lifecycle_doc_reconciliation_test.go` and
`slot_definition_glossary_reconciliation_test.go` are the only other files touching
state-machines.md, and both read the concurrent-occupancy section or the glossary Slot entry, neither
of which the new row enters. The staged tier-11 edits verify against the file as it stands:
`generalSlotEdges` is at :32-37, the positive loop at :55, the negative loop at :70, and the
`requireAllContain` over the doc section at :102-109, and the new row's text yields the paired
substring the proposal asserts.

USEFUL [Standing context → Traps, "Dead end, and here is its resolution: `docs/reference/adapter-contract.md:75` is the site and `:81` is not"]: this plus the orchestrator's refuted list is what kept
this round from spending itself re-filing the docs-corpus finding a dozen lenses have already filed.
Promote it until the site lands in an edit list or the DEFERRED is formally closed.

USEFUL [archive, non-spec-recheck.1.review-client-surface.1 and .review-edit-sites.1]: their FACTs
that `exited_cleanly` has no spec/docs/SDK mirror, that `lenny_adapter_leaked_slots` has no docs row,
and that `docs/reference/metrics.md:166-167` stays true after CODE-5 saved this round three separate
verification passes. I re-checked the SDK half and it still holds.


### [non-spec-recheck.5.review-docs-alignment.1]

DECISION: returned an EMPTY findings list — BECAUSE every docs surface this change touches
resolves to one of three closed buckets: DOCS-1 covers the only page a staged edit falsifies,
the `adapter-contract.md:75` row is the loop's one refuted docs finding, and the four
"at each session release" siblings are the recorded five-site dead end — ALTERNATIVES: I built
and dropped four candidates, each recorded below so nobody rebuilds them.

WATCHOUT: THE ROUND-5 SNAPSHOT IS USELESS. `diff -ru scratchpad/cp-snap/0081/non-spec-recheck-r5
proposals/0081_.../` returns ONLY review-log differences; every staged file is byte-identical.
The snapshot was taken at 15:11 while the last staging edit was 14:37, so it is a POST-delta
snapshot. Round 4 returned three empty findings lists (compaction changelog, review-log.md:5-13),
so there was no round-4 fix and the staging has not moved since round 3's fixes. Do not spend a
pass hunting a delta that does not exist; review the whole staging.
EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r5/ vs the proposal directory; `ls -la` mtimes.
This repeats `[non-spec-recheck.1.review-docs-alignment.1]`'s WATCHOUT one round later, so it is
a recurring orchestrator artefact rather than a one-off.

FACT: DOCS-1's tier-11 plan is mechanically correct and I verified every line citation in it.
`generalSlotEdges` is at tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37
(the proposal's `:32-36` covers the declaration and all four entries); the positive loop over the
either-concurrency block is at `:55`; the negative loop over the concurrent-occupancy block at
`:70`; the `requireAllContain` over the doc's `### Per-slot sub-states` section is at `:102-108`
(`:108` is the `` `released` `` entry, `:109` closes the call). Adding
`"receiving_uploads ──→ slot_cleanup"` to the slice satisfies both loops after SPEC-4, because
`scopedBlock` is sliced `[scopedHeader:generalHeader]` (`:64`) and SPEC-4 inserts into the general
fence. The claim that the unedited file still passes between S4 and S5 also holds: the negative
loop only forbids the OLD four edges in the scoped block.

FACT: the `**Slot cleanup:**` and `**Scrub model.**` anchors SPEC-3 rewrites are policed by NO
tier-11 or tier-0 gate. `grep -rn "Slot cleanup\|Scrub model" tests/tier11_docs/ tests/tier0_static/`
returns nothing. The nearby §5.2 gates key on OTHER lines and survive: `requireLine(s52, ...)` in
concurrent_slot_lifecycle_doc_reconciliation_test.go targets "Whole-pod replacement trigger" (:92),
"Session count limit" (:155), "Uptime limit" (:298) and the retirement-logging line (:222), and
recycle_scrub_trigger_consistency_test.go targets "The gateway triggers the whole-pod scrub" (:70).
SPEC-3's appended scrub-model text says "the whole-pod replacement trigger stated below" in
LOWERCASE, so `lineContaining` still returns the bullet rather than the appended paragraph. A fix
round that capitalises that phrase, or that adds "Session count limit"/"Uptime limit" wording to
the scrub-model paragraph, silently redirects those four gates to the wrong line, because
`lineContaining` returns the FIRST match and the scrub-model paragraph (spec/05:453) precedes all
of them. EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:150-165 (requireLine).

FACT: the §4.7 `Shutdown` row survives its two gates after SPEC-1, verified by reading the row at
spec/04_system-components.md:686 against them. `TestRecycleScrubTriggerAgrees` needs
"recycle disposition", "ReportPodScrub", `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`,
"does not block the response on the scrub" and the §5.2 link — ALL of these live in the retained
tail (from "On the default disposition the pod is replaced." onward), which SPEC-1 leaves alone,
and the new opening adds a second §5.2 link rather than removing one. `requireLine` also still
matches one line, because the replacement keeps the row on one physical line.
EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65,74-100,138-141.

FACT: SPEC-2's §7.2 edits do not disturb `per_slot_substate_scope_doc_reconciliation_test.go:78`
(`requireLine(s72, "per-slot sub-states")`). That phrase has exactly ONE occurrence in
spec/07_session-lifecycle.md, at :162, and none of the three §7.2 edits (preamble deletion, step-2
append, step-3 replacement) introduces a second. Since `lineContaining` returns the first match, a
future edit that adds the phrase earlier in §7.2 would silently redirect that assertion.

FACT: SPEC-3's new credential-path prose does NOT trip the tier-11 credential sweep. The swept
literal is `retiredPodGlobalCredentialPath = "/run/lenny/" + "credentials.json"`
(tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go:40), and SPEC-3
writes the directory and the filename as two separate backticked tokens
(`` /run/lenny/slots/{sessionId}/ `` and `` credentials.json ``), which never forms that substring.
Do not file it.

MISTAKE (nearly filed, and here is what killed it): "the placement constraint adds a new cause of
`WARM_POOL_EXHAUSTED` that docs/operator-guide/troubleshooting.md's cause table does not carry".
The table at docs/operator-guide/troubleshooting.md:36-44 enumerates warmup-failure causes only
(image pull, setup command, quota, node pressure, minWarm), and it is ALREADY incomplete against
the shipped tree: `ErrTenantMismatch` and uptime-expired candidates both surface as
`WARM_POOL_EXHAUSTED` today and appear in no row. One more instance of a pre-existing enumeration
gap does not clear the bar, and five lenses have already refuted themselves on the sibling
`concurrent_slots_exhausted` gloss (standing trap). EVIDENCE: docs/reference/error-catalog.md:143
("after exhausting both the API-server claim path and the Postgres fallback") is loose the same way.

MISTAKE (nearly filed): "`docs/reference/state-machines.md:251` glosses `slot_cleanup -> leaked`
as 'when the cleanup timeout is exceeded', and the proposal adds an unacknowledged reclaim as a
second cause". This is the docs mirror of the standing "Do not file either `leaked` gloss" trap
(§6.2:148 and §5.2:562 carry the identical gloss and §5.2's own bullet already says "If cleanup
fails, the slot is leaked", broader than either). The mirror inherits the trap.

MISTAKE (nearly filed): "`lenny_adapter_leaked_slots` has no docs/reference/metrics.md row and
CODE-5 adds a new feeder for it". The gauge is absent from docs entirely
(`grep -rn lenny_adapter_leaked_slots docs/` is empty) and the proposal adds no metric, so the
remedy would be pre-existing docs completeness. `[non-spec-recheck.1.review-docs-alignment.1]`
already recorded this; I re-verified it rather than trusting it.

USEFUL [non-spec-recheck.1.review-docs-alignment.1]: its four FACTs (the policed-but-presence-only
`adapter-contract.md:75` gate, the four-way falsification split, the absent leaked-slots doc row,
and the concurrency-safe metrics.md:166-167 rows) covered most of this lens's search space and let
me spend the round on the tier-11 gate mechanics instead of rediscovering them. Its finding was
subsequently refuted by the material skeptic; the FACTs stand regardless.

USEFUL [Traps, "Dead end, and here is its resolution: `docs/reference/adapter-contract.md:75` is
the site and `:81` is not"]: the entry plus the refutation together mean the whole adapter-contract
family is now closed in BOTH directions. A future docs lens should treat `docs/reference/` as swept
and spend its round on `docs/operator-guide/` and `docs/runbooks/`, which I found carry no
narrative that this change falsifies (no runbook or operator page enumerates pod-retirement or
slot-leak causes; `grep -rln "pod retirement\|unhealthy threshold\|maxSessionsPerPod"` over
docs/runbooks/ returns nothing).


### [non-spec-recheck.5.review-edit-sites.1]

DECISION: returned an EMPTY findings list — BECAUSE every surface this lens owns came back clean or already-adjudicated, and the one genuine gap (`docs/reference/adapter-contract.md:75`) is the standing DEFERRED that eleven lenses have filed and that the material skeptic refuted on the materiality bar. ALTERNATIVES rejected: (a) refiling the adapter-contract:75 site — barred as re-litigation, the refutation is in the orchestrator's refuted list verbatim; (b) filing `tests/spec-map.json` as a missing edit site — see the FACT below; (c) filing the `WARM_POOL_EXHAUSTED` / `concurrent_slots_exhausted` gloss in `docs/reference/error-catalog.md:143` — same class as the closed dead-end at spec/05:549, the docs row is already loose against the shipped `ErrTenantMismatch` and `expiredByUptime` skips.

FACT: the full new-identifier sweep is DONE and clean; do not re-run it. `grep -rn "ExcludePod\|slot_bind_failed\|runtimeHolds\|slotCleanupBudget\|accountSlotFailure\|materializeSlotStages\|compensateFailedSlotBind" spec/ docs/ schemas/ charts/` returns ZERO hits. Every identifier this proposal adds is in-process Go; none reaches a wire format, a manifest key, a flag, an env var, a metric name, an alert name, a Helm value, or a yaml key. EVIDENCE: that grep, run at commit f2a397b53.

FACT: the staged-anchor sweep now has a mechanical, repeatable form. A 10-line python script over the 28 fenced blocks in `.spec-changes.md` confirms every "text to replace" block occurs EXACTLY ONCE across `spec/*.md` and every "replace it with" block occurs ZERO times. Blocks 0,2,5,8,10,12,14,16,18,20,22,24 are the anchors (each count 1); 1,3,4,6,7,9,11,13,15,17,19,21,23,25,26,27 are replacements/insertions (each count 0). EVIDENCE: `proposals/0081_.../0081_....spec-changes.md` fenced blocks vs `spec/`.

FACT: `spec/16_observability.md`, `docs/reference/metrics.md`, `pkg/alerting/rules` and `docs/runbooks/` are NOT edit sites and the reason is structural rather than incidental: there is no metric row, no alert, and no runbook for leaked slots, per-slot cleanup, or session scrub anywhere in the corpus. `lenny_adapter_leaked_slots` — the one gauge the staged §5.2 append names — is registered only in spec/05:545, spec/06:160 and `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:223`, and it is absent from the §16 inventory today. So SPEC-3's naming of it mints nothing. EVIDENCE: `grep -rn "lenny_adapter_leaked_slots" spec/ docs/ pkg/`; `ls docs/runbooks/`.

FACT: the §6.2 companion triple is fully staged and the tier-11 gate mechanics check out end to end. `generalSlotEdges` is a fixed 4-string slice at `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37`, driving a positive loop over `generalBlock` at `:55-59` and a NEGATIVE loop over `scopedBlock` at `:70-74`. Adding `"receiving_uploads ──→ slot_cleanup"` passes both, because `scopedBlock` (spec/06:146-148) carries only `running ──→ failed` and `slot_cleanup ──→ leaked` and no substring of either matches the new edge. DOCS-1's row is spelled `` | `receiving_uploads` | `slot_cleanup` | `` so the staged `requireAllContain` pair-substring matches verbatim. EVIDENCE: that test file; docs/reference/state-machines.md:232-237; spec/06_warm-pod-model.md:146-155.

FACT: the two call-site inventories a signature change reaches are both complete in the staging. `ReleaseSlotReservation` has exactly six production sites plus the interface plus two fakes (`start.go:2727` interface, `:2834` applySlotRetryPolicy, `:3246` rollbackClaim, `slotbinder.go:172` connect-stage, `:217` BindReservedSlot, `binder.go:1714` releaseResumeSlot, `slotretry_test.go:52`, `slotretry_load_test.go:33`) — every one is in CODE-4's table or the Tests list. `noteRuntimeStarted` has exactly ten (3 production + 7 test) and CODE-2 enumerates all ten. EVIDENCE: `grep -rn "ReleaseSlotReservation" pkg/`; `grep -rn "noteRuntimeStarted" pkg/ tests/`.

FACT: `noteRuntimeStartedLocked` is called from exactly one place, `noteRuntimeStarted` (runtimegeneration.go:32). So the tier-7a paragraph's "`noteRuntimeStarted` is the only writer of `runtimeLive`" is true as written even though the write physically happens in the `Locked` form. A reviewer who reads Settled's "writers are exactly two (`noteRuntimeStartedLocked` and `noteRuntimeClosed`)" may think the paragraph mis-attributes; it does not. EVIDENCE: pkg/adapter/runtimegeneration.go:26-49,:58-68.

FACT: the round-4 tier-7a rewrite's citations all resolve. session.go:156 is `s.Runtime.Start`, :163 is `s.noteRuntimeStarted`; resume.go:140/:144 the same pair; adapterevents_test.go:95 and :184 are the two bare-`New("served")` record sites; adapterevents.go:153-155 is the `soleSession` fallback; binder_test.go:301/:322 are `fakeAssigner.released`'s comment and its append. The edit-sites lens did NOT run in round 4, so this paragraph was new to it; it is now checked. EVIDENCE: those files.

WATCHOUT: `pkg/gateway/podlifecycle/podsession/binder_test.go` and `resume_slot_reservation_test.go` are in no file list, and BOTH are correct omissions rather than gaps. `binder_test.go` holds `fakeAssigner` but a new case can live in the same package's `slotbinder_test.go` and use it unchanged; `resume_slot_reservation_test.go`'s `TestResumeReleasesItsReservationWhenTheAdapterResumeFails_spec_5_2` stays green because its adapter holds no entry for the session, so the compensating `Shutdown` answers cleanly, `Leaked` is false, and `releaseResumeSlot` decrements as today. Do not file either as a missing edit site without new evidence that a listed assertion actually breaks. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:295-323; resume_slot_reservation_test.go:212-232.

WATCHOUT: the snapshot the brief hands you is again empty. `diff -rq scratchpad/cp-snap/0081/non-spec-recheck-r5 proposals/0081_.../` differs on the review log ALONE, and so does `-r4`. The last real staging delta is r3→r4: the tier-7a paragraph rewrite at non-spec-changes.md:829-872. Diff against `non-spec-recheck-r3`, not r5. This is the fifth consecutive round where the named snapshot was byte-identical; Settled already says so and it held again.

UNVERIFIED: `tests/spec-map.json`'s own `description` field says "every PR that touches spec/, pkg/, schemas/, migrations/, or charts/lenny/ updates this file", and 0081 touches spec/ and pkg/ heavily, adds tests under `tests/tier11_docs/`, `tests/tier4_integration/` and `tests/tier7a_load_local/`, and names the file nowhere. I declined to file it: `lenny-test validate-maps` checks only existence, version and dangling paths, so nothing goes red, and the map's §4.7/§5.2/§6.2/§7.1 entries become INCOMPLETE rather than incorrect — every path they already list stays valid. Sibling proposal 0079 does list it, so the practice is inconsistent across proposals. A conformance lens after implementation, or a human setting the convention, should settle it once for the campaign rather than per proposal. EVIDENCE: tests/spec-map.json:3.

UNVERIFIED: CODE-2's replacement doc comment for `noteRuntimeStarted` drops the shipped comment's idempotency sentence ("It is a no-op when the generation's first session is already this session, so an idempotent repeat of a start cannot raise the cohort and drive soleSession empty for the life of the pod"). The BEHAVIOUR is untouched, because the guard lives in `noteRuntimeStartedLocked` (runtimegeneration.go:40-43) which CODE-2 does not edit. Judged below the bar as doc-comment content rather than an edit site. Someone landing CODE-2 should keep the sentence, or move it onto the `Locked` form. EVIDENCE: pkg/adapter/runtimegeneration.go:22-33 versus non-spec-changes.md:215-227.

USEFUL [Settled, "The snapshot diffs have been empty for most rounds"]: saved a whole round. Without it I would have hunted a fix-stage delta in text that has not moved since r4.
USEFUL [Traps, #278 bookkeeping-against-the-deliverable-index]: killed three candidates before I spent verifier pairs on them.
USEFUL [Deferred, docs/reference/adapter-contract.md]: the entry's CORRECTED half — that :81 and the four sibling "at each session release" sites are NOT edit sites while :75 is — is what let me sweep `grep -rn "per-slot cleanup" docs/` in one pass and stop.


### [non-spec-recheck.5.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor-action assignment in the
staging resolves against the tree: the adapter owns every predicate CODE-1/CODE-2 read
(`st.started`, `runtimeLive`, `s.slots`), the gateway owns every collaborator CODE-4/CODE-5
name, and each layer holds the data its check needs at the site the staging names.
ALTERNATIVES: I built and dropped four candidates, each recorded below so nobody rebuilds them.

FACT: the snapshot at `scratchpad/cp-snap/0081/non-spec-recheck-r5` is byte-identical to the
live proposal for EVERY file except the review log. `diff -ru --exclude='*review-log*'` returns
nothing. The orchestrator's "the staging changed after this lane's last review converged" was
not observable, exactly as the standing Settled entry warns. Do not spend a round hunting a
fix-stage delta. EVIDENCE: `diff -ru --exclude='*review-log*' scratchpad/cp-snap/0081/non-spec-recheck-r5 proposals/0081_.../` → empty.

FACT: `resumeOnPod`'s checkpoint-restore failure branch has everything CODE-5's third caller
needs in scope — `ctx`, `match` (so `match.Pool` and `maxConcurrentSessions(match.MaxConcurrentSessions)`),
and the `*Server` receiver carrying `podBinder`, `slotHealth`, `slotStates`, `slotReplacement`,
`slotLeakGauge`. `s.podBinder` is a concrete `*podsession.Binder` and satisfies `slotBinder`
(BindSlot / ReleaseSlotReservation / DrainSandbox). EVIDENCE: pkg/gateway/sessionserver/start.go:4005-4043;
sessionserver.go:188,:401-421,:2736.

FACT: `Binder.Resume`'s adapter-RPC failure branch holds `cl` (still open), `sb.Name` and
`slotID` at the moment CODE-4 inserts the compensation, so "on the still-open connection,
then the close, then the release" is implementable verbatim. EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:1591-1631.

FACT: `ReleaseSlotReservation` has exactly five production call sites plus the `slotBinder`
interface declaration and two test fakes, and CODE-4's call-site table names all of them.
EVIDENCE: `grep -rn ReleaseSlotReservation pkg/ cmd/` → slotbinder.go:172,:217,:493;
binder.go:1714; start.go:2727,:2834,:3246; slotretry_test.go:52; slotretry_load_test.go:33.

FACT: every adapter-test citation CODE-2 makes is exact. `export_test.go:45` is
`s.noteRuntimeStarted(sessionID)` inside `ClaimSessionForTest`; `adapterevents_test.go:95` and
`:184` are the two bare `New("served")` record sites; `bindSessionForTest`
(usage_test.go:350-363) sets `WorkspaceBase` when empty and sets both `st.sessionID` and
`st.started`, so it satisfies CODE-2's guard. `TestEmitFinalUsageOnShutdownPath_spec_4_7`
calls `emitFinalUsage` DIRECTLY (adapterevents_test.go:192) rather than through `Shutdown`,
which is why the proposal's "stays green either way" is right despite the test's name.

FACT: no tier-11 gate breaks on SPEC-1's §4.7 row replacement, including the two the standing
Settled entry does not name. `spec_47_rpc_row_naming_test.go` only harvests the leading
`` | `Name` | `` cell (spec_47_rpc_row_naming_test.go:35,:44-54), which the replacement keeps,
and `concurrent_slot_lifecycle_doc_reconciliation_test.go` pins §5.2/§6.2/§10/state-machines
lines this proposal does not touch. `recycle_scrub_trigger_consistency_test.go` needs
"recycle disposition", "ReportPodScrub", `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`,
"does not block the response on the scrub", and the §5.2 link — every one of which sits in the
retained remainder from "On the default disposition the pod is replaced." onward. EVIDENCE:
tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-101,:137-147.

FACT: `Registry.MarkLeaked` seeds an untracked slot at `Leaked` and never consults the edge
list, so CODE-5's two new callers cannot raise an invalid-transition error for a slot the
gateway never `Assign`ed. EVIDENCE: pkg/sandbox/slotstate/registry.go:99-114.

MISTAKE (nearly filed, killed by the tree): "the SDK-warm freshness arm leaves `started &&
!live` reachable at an ordinary session end, so CODE-1 would withhold `ReportSessionScrub`
for every SDK-warm session." `fresh` is false at sdkwarm.go:221/:260 only when
`claimSessionSlotUnderLock` short-circuits on `st.started` under `idempotentRepeat`
(slotsession.go:80-86), i.e. a REPEAT `ConfigureWorkspace` for a session whose first, fresh
call already recorded. So `runtimeLive` holds it and `live` is true. EVIDENCE:
pkg/adapter/sdkwarm.go:217,:221,:249-262; pkg/adapter/slotsession.go:64-91.

MISTAKE (nearly filed): "`ExitedCleanly: closeErr == nil && (live || treeErr == nil)`
reclassifies an ordinary session end as leaked when `removeSlotTree` fails." It does not:
`live` is true for every session the runtime was given, so the disjunct short-circuits and the
answer is `closeErr == nil` exactly as today. The staging's `live`-not-`started` choice is what
makes that hold for a start still in flight too.

MISTAKE (nearly filed): "`cancelPodMCPIfRuntimeIdle` moving into the `removed` branch cancels a
co-tenant's armed MCP surface." Double-guarded by `runtimeIdleLocked()` and
`mcpArmingHeldLocked()`, and the only arm it newly reaches is a reclaim of the session that
armed the surface itself. EVIDENCE: pkg/adapter/slotsession.go:238-260.

WATCHOUT: `slotCleanupBudget`'s stated formula divides by `maxConcurrentSessions`, and the doc
comment addresses only an unset `cleanupTimeoutSeconds`. Every reachable caller supplies a
value ≥ 1 (`materializeSlot` runs only under `MaxConcurrentSessions > 1`; `resumeOnPod` passes
`maxConcurrentSessions(match.MaxConcurrentSessions)`, which floors at 1), so a zero divisor is
unreachable in production. A test fake that leaves the field zero would panic. Implementor
detail, below the bar as a finding. EVIDENCE: non-spec-changes.md:333-339;
pkg/gateway/sessionserver/start.go:4029.

USEFUL [Settled: "Every code target in the non-spec staging exists with the stated signature"]:
it named `materializeSlot` (slotbinder.go:265), `resumeOnPod`'s branch (start.go:4041-4043),
`connectSlot`'s `podclaim.SlotRequest` mapping (slotbinder.go:422-428) and the two
`expiredByUptime` skip sites (:433, :491) with line numbers that all still resolve. I re-checked
four of them and every one was exact; that saved most of a pass.

USEFUL [Settled: "The snapshot diffs have been empty for most rounds"]: it told me to run
`diff -rq` first and not to hunt a delta. Correct again this round.


### [non-spec-recheck.5.review-fresh.1]

FACT: the snapshot named in this round's brief (`scratchpad/cp-snap/0081/non-spec-recheck-r5`) is byte-identical to the live proposal for every file except the review log, so `diff -ru` returned only review-log noise. The standing WATCHOUT about empty snapshot diffs (review-log.md:125) held again. The actual delta had to be read out of the `## Index and checklist reconciliation (post-spec-loop)` block at review-log.md:547-676. — EVIDENCE: proposals/0081_.../0081_....review-log.md:125,:547

FACT: `ExcludePod` is set on `applySlotRetryPolicy`'s own value copy of `req`, and `applySlotRetryPolicy` is NOT the outermost placement loop. `bindConcurrentSlot` wraps `bindSlotWithRetry` in `runWithQueue` (pkg/gateway/sessionserver/start.go:2606-2609); `runWithQueue`/`waitInQueue` re-invoke the captured closure on an exhaustion sentinel (pkg/gateway/sessionserver/queue.go:136,:143-146,:205), and the closure rebuilds the call from `bindConcurrentSlot`'s own `slotReq`, whose `ExcludePod` is the zero value. `isExhaustion` covers `ErrNoConcurrentSlot` (queue.go:103-107), which is exactly what the exclusion-driven `ClaimSlot` returns when the excluded pod was the pool's only candidate. This is the one finding filed this round. — EVIDENCE: pkg/gateway/sessionserver/start.go:2606-2609,:2735-2736,:2807,:2816-2819; pkg/gateway/sessionserver/queue.go:103-107,:143-146,:205

FACT: `maxSlotRetries` is a hard-coded `const = 1` in `pkg/gateway/sessionserver/start.go:2720`, not read from `sessionPolicy.slotRetries`. That matters for `ExcludePod`: with exactly two attempts per `applySlotRetryPolicy` call, a single-string exclusion is sufficient WITHIN one call, so the only bypass is the queue re-entry above. Do not file a "single-string ExcludePod cannot hold two pods" finding; it is unreachable at the shipped budget. — EVIDENCE: pkg/gateway/sessionserver/start.go:2720,:2809

FACT: the six `ReleaseSlotReservation` call sites in the tree are exactly the six the CODE-4 call-site table lists (slotbinder.go:172 ClaimSlot connect-stage, slotbinder.go:217 BindReservedSlot, binder.go:1714 releaseResumeSlot, start.go:2727 interface, start.go:2834 applySlotRetryPolicy, start.go:3246 rollbackClaim). The table is complete; do not re-check it. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:172,:217,:493; pkg/gateway/podlifecycle/podsession/binder.go:1714; pkg/gateway/sessionserver/start.go:2727,:2834,:3246

FACT: `noteRuntimeStarted` has exactly ten call sites tree-wide (3 production: session.go:163, resume.go:144, sdkwarm.go:261; 7 test: export_test.go:45, usage_test.go:233, podmcp_arming_internal_test.go:84/:185/:230, adapterevents_test.go:95/:184). CODE-2's call-site scope names every one of them correctly. Also: in Go, `s.noteRuntimeStarted(x)` as a bare statement still compiles once the function returns a bool, so the `_ =` rewrites the proposal specifies are style, not a compile requirement — the "No `export_test.go` change" sentence at non-spec-changes.md:638 and the `export_test.go:45` discard at :289 are therefore in tension only stylistically. NOT filed. — EVIDENCE: pkg/adapter/runtimegeneration.go:26; pkg/adapter/export_test.go:45

WATCHOUT: `tests/tier4_integration/concurrent_workspace_test.go` sets no `srv.Lifecycle`, so the staged tier-4 case's assertion "alice's later `Shutdown` still emits the §15.4.2 signal" needs a `startRuntimeOps`-style CH-RUNTIMEOPS fixture the file does not have, and the file drives the adapter over raw gRPC with no gateway `Binder` (so "the compensation" is a direct `Shutdown` RPC). I did NOT file it: the near-identical tier-4 fixture finding on `recycle_scrub_path_test.go` was already refuted as ordinary fixture extension. — EVIDENCE: tests/tier4_integration/concurrent_workspace_test.go:108-125 (no `srv.Lifecycle`)

FACT (verified, so nobody re-derives it): every SPEC anchor in spec-changes.md matches the tree verbatim — spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown` row opening), spec/04:854 (§4.7.9 step 5), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**`), spec/06:150-155 (per-slot fence) and :234 (mid-resume cancel bullet), spec/07:23-24 (atomicity paragraph inside the 5..54 fence) and :210/:213/:214 (§7.2 preamble, step 2, step 3) and :414 (§7.3 list tail), spec/29:711 (step 13 tail). — EVIDENCE: as listed

FACT: the staged tier-11 edit is sound. `generalSlotEdges` at tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37 feeds a positive loop over the either-concurrency block (:55) and a negative loop over the concurrent-occupancy block (:70); the scoped block (spec/06:146-148) carries only `running ──→ failed` and `slot_cleanup ──→ leaked`, so adding `receiving_uploads ──→ slot_cleanup` to the slice passes both loops after S4. Between S4 and S5 the unedited file still passes, as the proposal claims. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55,:70; spec/06_warm-pod-model.md:146-155

USEFUL [Traps / "The snapshot diffs have been empty for most rounds"]: saved the whole snapshot-diff detour; the entry is still accurate and should be kept.


### [non-spec-recheck.5.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE every Kubernetes-idiom axis the lens names is
either untouched by the staging or already adjudicated, and each candidate I built collapsed into a
family the loop has already refuted — ALTERNATIVES: I built and dropped four candidates, each
recorded below so nobody rebuilds them.

USEFUL [Settled #46 "Ownership is clean"]: it is exactly right and it is the whole lens in one line.
I re-derived it from the tree anyway and it holds end to end: the gateway's only kube writes on
every path this proposal touches are (1) `SandboxClaim` spec/status, which §4.6.3 assigns to the
gateway as sole writer, and (2) a JSON-merge PATCH of the agent Pod's `lenny.dev/drain-request`
annotation under `client.FieldOwner(ownership.Gateway)`. No component writes another's status, no
SSA `Force`, no finalizer anywhere on `SandboxClaim` (it deliberately carries no ownerReference
either). EVIDENCE: spec/04_system-components.md:606-621 (ownership table + gateway RBAC paragraph);
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:95-108 (StampDrainRequest);
pkg/gateway/podlifecycle/podsession/slotbinder.go:601-603 (DrainSandbox doc comment states the
§4.6.3 routing).

FACT: the shipped `ClaimSlot` candidate passes carry a worked precedent for exactly the read-only
placement filter CODE-5 adds, including the K8s-ownership assertion. `expiredByUptime`
(slotclaimer.go:336-357) is skipped with one `continue` in each pass (pass 1 at :417-441, pass 2 at
:483-500), and its two envtest tests assert not only the skip but that the gateway left
`Sandbox.status.phase` untouched and is not a status field manager
(`assertNotGatewayStatusOwned`). EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go:648-716.
`ExcludePod` is the same read-only shape and writes nothing, so it raises no ownership question.

FACT: pass 1 reads the per-pod `SandboxClaim` (a GET per Sandbox in the pool list) and never
`Sandbox.status.phase`; pass 2 reads `sb.Status.Phase != Idle` and then GETs the claim. The
proposal's statement that the drain annotation does not keep an immediate retry off the pod is
therefore exactly right, and `ExcludePod` is the only thing that does.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:414-441, :481-505.

MISTAKE (nearly filed, candidate 1): "a `leaked=true` release leaves the per-pod `SandboxClaim`
permanently `bound`, which is a stuck etcd object with no reconciler". It does leave the claim —
`ReleaseSlot` returns at slotclaimer.go:830-836 before the counter decrement, the recycle patch and
the `DeleteClaim` at :885 — but the backstop is real and level-triggered:
`ClaimGarbageCollector.classify` reclaims a `bound` claim by draining once
`Status.BindingStateTransitionTime` (falling back to `CreationTimestamp`) ages past
`claimOrphanTimeout`, gated on `Sessions.PodHasActiveSession` returning false.
EVIDENCE: pkg/controller/warmpool/gc.go:223-320. This is the already-refuted occupancy-hold family;
the only live residue is standing Open #88 (the GC's `PodHasActiveSession` gate is defeated once
`ClaimSlot` pass 1 places a same-tenant sibling on the pod), which is bounded by the leak threshold
and `maxPodUptimeSeconds` and is not filable on this evidence.

MISTAKE (nearly filed, candidate 2): "the resume path's compensation runs on an exclusive pool where
`releaseResumeSlot` no-ops and CODE-5's non-empty-slot-id guard suppresses the accounting, so a
bricked replacement pod is neither released nor counted". Verified true as mechanism
(`reserveResumeSlot` returns "" at binder.go:1674-1676; `releaseResumeSlot` no-ops at :1710-1713;
`resumeOnPod`'s failure branch is a bare `return "", err` at start.go:4041-4043 with no
`rollbackBinding`), but it is the pre-existing gap standing Open #68 records, it is bounded by the
same §4.6.1 orphan GC, and the "§7.1's exclusive disposition is unreachable on the §7.3 re-attach"
finding was already built and refuted on this ground.

MISTAKE (nearly filed, candidate 3): "the new callers stamp `lenny.dev/drain-request` and then
release the claim, so the pod can be re-claimed before the WarmPoolController acts". The stamp is an
annotation the WPC consumes level-triggered and idempotently, and `applySlotRetryPolicy` already has
the identical shape today; convergence is the WPC's job, not the gateway's. Also already declined as
"tier 5 is reached" in Traps.

MISTAKE (nearly filed, candidate 4): "the staged tier-2 placement-filter cases omit the
`assertNotGatewayStatusOwned` assertion the two precedent tests carry". `ExcludePod` writes nothing,
so the assertion would be pure defence in depth — an additional nice-to-have test, which the bar
excludes.

FACT: the delta the brief promised does not exist. `diff -rq scratchpad/cp-snap/0081/non-spec-recheck-r5
proposals/0081_.../` reports ONLY the review log differing, and that difference is compaction pass 4
rewriting `## Standing context`. The staging text (`non-spec-changes.md`, `summary.md`,
`spec-changes.md`, the checklist) is byte-identical to the snapshot. This is the fourth consecutive
round in which that is true; Settled #154 already says so. Do not spend a round hunting a fix-stage
delta.

FACT (citation spot-checks, all clean, do not re-run): `scrubreport_server.go:451-481`
(`RecordSessionScrub` increments `sessions_served` before branching on `leaked`, no per-session
dedup) ✓; `slotbinder.go:542-543` (`leaked = err != nil || !cleanly`) ✓; `slotbinder.go:487-504`
(`ReleaseSlotReservation` hard-codes `recycle=false, leaked=false`, and ignores its own `slotID`
parameter entirely) ✓; `slotclaimer.go:830-836/:845-847/:850-878/:881-885` (the three release
dispositions plus the leaked early return) ✓; `session.go:238-241,:243,:259,:271-279,:291` (the
shipped `Shutdown` clause two the CODE-1 block replaces) ✓; `slotsession.go:214-220`
(`releaseSessionSlot`) ✓; `resume.go:134,:141,:140,:144` ✓; `connectSlot`'s `podclaim.SlotRequest`
mapping at `slotbinder.go:423-429` (the proposal cites :422-428, off by one at both ends and inside
the real span) ✓; `start.go:2727-2730` (`slotBinder` carries `DrainSandbox`, so CODE-5's third caller
passing `s.podBinder`, a concrete `*podsession.Binder`, satisfies it) ✓; `start.go:4041-4043`
(`resumeOnPod`'s Resume failure branch) ✓.

FACT: `bindReservedSlot`'s three connect-stage failures (`resolveSandbox`, `DialAdapter`,
`NegotiateVersion`) return a `SlotBindError` WITHOUT reaching `materializeSlot`, so CODE-4's
compensation never runs there and `sbe.Leaked` stays false until `BindReservedSlot`'s own release
error sets it. That is exactly what the "connect stage compensates nothing" edge-case bullet says.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:230-254, :207-224.

OPEN: nothing new. Standing Open #88 (orphan GC defeated by a sibling session) and #68 (no pod-claim
release on the exclusive resume path) are the two items a Kubernetes lens would want a human to
settle; both are pre-existing and neither is filable against this proposal's staged text.


### [non-spec-recheck.5.review-mechanism.1]

DECISION: returned an EMPTY findings list — BECAUSE I traced every flow the staging describes end to
end against the tree (adapter `Shutdown` three-predicate split, CODE-2's confirm/rollback on both
start RPCs, the `materializeSlot` wrapper's compensation + lease release, `ReleaseSlotReservation`'s
new disposition through `SlotClaimer.ReleaseSlot`, the three `accountSlotFailure` callers, and
`ExcludePod` from `applySlotRetryPolicy` through `connectSlot` into `ClaimSlot`'s two passes) and
every candidate I built either checked out or collapsed into a class this loop has already refuted.
ALTERNATIVES: five candidates built and dropped, itemised below.

FACT: THERE IS AGAIN NO SNAPSHOT DELTA. `diff -rq scratchpad/cp-snap/0081/non-spec-recheck-r5 <proposal> -x '*review-log*'`
is empty, and so are `-r5-start`, `-r4` and `-r4-start`. The newest snapshot that differs is
`non-spec-recheck-r3`, whose only delta is the tier-7a paragraph (non-spec-changes.md:829-872).
The staging text has not changed since round 3. This is the FIFTH consecutive round in which the
orchestrator's "the staging changed" is not observable; the standing-context entry predicting it is
correct and should be trusted rather than re-derived.

FACT (verified this round, do not re-derive): the whole `slotCleanupBudget` divide is safe.
`slotBindRequest` sets `MaxConcurrentSessions: match.MaxConcurrentSessions` UNNORMALISED
(start.go:3391 region), but `bindConcurrentSlot` is the only consumer and is reachable only under
`match.MaxConcurrentSessions > 1`, and the resume path normalises through `maxConcurrentSessions()`
(start.go:3353, used at :4029). So `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` cannot
divide by zero at any staged call site. EVIDENCE: pkg/gateway/sessionserver/start.go:3353,:4029;
:2594 (`bindConcurrentSlot`).

FACT: `cl.ConfigureWorkspace` has EXACTLY ONE production caller, `Binder.Launch` at
pkg/gateway/podlifecycle/podsession/binder.go:1009. CODE-2's justification for giving `sdkwarm.go:261`
no rollback ("no compensation races it") therefore holds against the tree, and a lens tempted to file
"the SDK-warm start can be raced too" should stop here. EVIDENCE: `grep -rn ConfigureWorkspace pkg/ cmd/ sdks/`
returns one `cl.ConfigureWorkspace` call site.

FACT: `BindReservedSlot`'s release covers BOTH the connect stage inside `bindReservedSlot`
(resolveSandbox / DialAdapter / NegotiateVersion, slotbinder.go:230-254) and every `materializeSlot`
stage, because the release sits in the outer `BindReservedSlot` wrapper (slotbinder.go:210-224). A
connect-stage failure there carries `sbe.Leaked == false` because `materializeSlot` never ran, so
CODE-4's `sbe.Leaked` value at that site is well-defined on every arm. Ordering is also sound:
`ReleaseSlotReservation` is passed the compensation's `sbe.Leaked`, and only afterwards does the
method raise `sbe.Leaked = true` on a release error, so the two writes never fight.

FACT: `slotBinder`'s only implementors are `*podsession.Binder` (concrete field
`podBinder *podsession.Binder` at sessionserver.go:188) plus the two named fakes
(slotretry_test.go:52, slotretry_load_test.go:33). `grep -rn ReleaseSlotReservation` over the module
returns exactly the five production call sites plus the interface line the proposal lists, so
CODE-4's signature change has no unlisted implementor. EVIDENCE: start.go:2727,:2834,:3246;
slotbinder.go:172,:217,:493; binder.go:1714.

FACT: the tier-11 gate arithmetic works as staged and I re-derived it independently. The negative
loop runs over `scopedBlock := s62[idx(scopedHeader):idx(generalHeader)]`, which carries only
`running ──→ failed` and `slot_cleanup ──→ leaked`, so adding `"receiving_uploads ──→ slot_cleanup"`
to `generalSlotEdges` passes both loops. The doc row spelling in the tree is
`| `receiving_uploads` | `running` | …` (docs/reference/state-machines.md:235), so DOCS-1's row
matches the staged `requireAllContain` pair byte for byte. EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55,:63-70,:102-108;
docs/reference/state-machines.md:232-237.

MISTAKE (mine, nearly filed): "the design's race analysis is binary and misses the middle ordering —
the reclaim's DEREGISTRATION precedes the start's claim while its `removeSlotTree` runs after it, so
the claim re-creates entry and tree and the reclaim then deletes the tree the new claim made."
The middle ordering is real (deregisterSlotLocked runs under `s.mu`, `removeSlotTree` outside it,
slotsession.go:174-188 / session.go:270-273) and the accepted-failure-mode bullet's words
"re-creates the registry entry and the slot tree" are incomplete for it. Dropped: the same window is
already written out in full in CODE-5's "When it does not fire" bullet
(non-spec-changes.md:588-597), the outcome class is identical (a started session for an abandoned
bind), the bullet lives in a section never applied to `spec/`, and this loop already refuted a
structurally identical "the edge-case bullet over-generalises" finding on the materiality bar.

MISTAKE (mine, nearly filed): "CODE-4's unconditional `b.releaseCredentials(req.SessionID)` in the
`materializeSlot` wrapper releases leases a SNAPSHOTLESS RESUME-REBUILD's session may still hold from
an earlier bind, which is exactly the hazard the proposal cites to exempt `Binder.Resume`." The
premise is real — the resume-rebuild reaches `materializeSlot` through
`resumeOnPod → startOnPod → bindConcurrentSlot → applySlotRetryPolicy → BindSlot`, and
`Credentials.ReleaseSession(sessionID)` is session-keyed rather than attempt-keyed
(binder.go:1263-1268). Dropped: the rebuild's own `assignSlotCredentials` re-mints on every attempt
(slotbinder.go:370-402), the session is not running while its pod is being rebuilt, and the only
residual harm needs a checkpoint-restore `Binder.Resume` to follow a failed snapshotless rebuild of
the same session, which no single request sequence produces. Also barred by the standing trap "Do NOT
make the bind-path lease release conditional".

MISTAKE (mine, nearly filed): "CODE-3's staged doc-comment gloss `(bind abandoned before the runtime
is given the session)` drops the `or fails` conjunct the SPEC-4 annotation, the DOCS-1 row and
SPEC-3's canonical range phrase all carry." True as stated (non-spec-changes.md:310 versus
spec-changes.md:498-500 and :619). Dropped as the same class as the standing trap "Do not file the
`slot_cleanup ──→ released` annotation": an abbreviated gloss in a Go comment states nothing false,
nothing tests it, and no behaviour turns on it.

MISTAKE (mine, nearly filed): "the compensation is sent for an upload-free `stageWorkspace` failure,
where NO pod-side RPC for the session was issued, so the code sends a reclaim staged §7.1's
'the obligation begins with an attempt's first such RPC' does not require." Dropped: over-sending an
obligation is not a violation of it, the staged §4.7 no-op sentence makes the extra send safe, and
the proposal states the case in three places (summary.md:114-118, non-spec-changes.md:424-429,
spec-changes.md:98-104).

MISTAKE (mine, nearly filed): "the §29.4 step-13 sentence scopes the co-tenancy gate to 'a pod
serving concurrent sessions' while the shipped `!boundRemains` gate and the §4.7 row state it
unconditionally." Dropped: on a one-session pod a bound co-tenant cannot exist, so the scoped
statement is vacuously equivalent, and the operative clause is verbatim §4.7's.

USEFUL [standing context, Settled #125 on snapshot diffs]: `diff -rq` over the whole snapshot
directory first. It saved a full round of hunting a delta that does not exist.
USEFUL [standing context, Traps]: the "eight-times-refuted §7.1-exclusive-pod-versus-§6.2:283" trap
and the "do not file either `leaked` gloss" trap each killed a candidate before I spent a verifier
pair on it.


### [non-spec-recheck.5.review-operational.1]

DECISION: returned an empty findings list — BECAUSE every operational surface the staging
touches (metric inventories, alert catalog, runbooks, per-slot doc table, drain/retirement
counters) reconciles after application, and the two candidates I built collapsed on the
materiality bar (details below) — ALTERNATIVES: filing the `sessionserver.go` doc-comment
narrowing, and filing the `lenny_slot_pod_replacement_total` label divergence; both rejected,
reasons under FACT/MISTAKE below.

FACT: The observability sweep is genuinely clean and here is the whole evidence set, so nobody
re-runs it. No alert in `pkg/alerting/rules/rules.go` references any slot metric (`grep 'slot'`
over that file returns only the §16.5 comment at :57 and unrelated `*Exhausted` names);
`WarmPoolExhausted` keys on `lenny_warmpool_idle_pods` (rules.go:283) and `WarmPoolLow` on the
same gauge (:687). No runbook under `docs/runbooks/` mentions a slot except three unrelated
hits (`redis-failure.md:66` Redis hash slots, `token-store-unavailable.md:127` a replication
slot, `ephemeral-container-cred-guard-unavailable.md:32` the credential path). spec/16 rows
:14 and :15 (`lenny_slot_failure_total`, `lenny_slot_pod_replacement_total`) and
`docs/reference/metrics.md:166-167` both stay true after CODE-5, because every new
`accountSlotFailure` caller is reachable only at `maxConcurrentSessions > 1`. This confirms
standing Settled entry "spec/16 needs no edit and can orphan no alert" by a second route.

FACT: The staged SPEC-3 clause "is surfaced on the `lenny_adapter_leaked_slots` gauge" is TRUE
on every path its antecedent binds, and the gauge is wired in production. Verified end to end:
`cmd/lenny-gateway/sessionsrv.go:378,:381-382` wires `SlotReplacement` and `SlotLeakGauge` to
`gwMetrics.IncSlotPodReplacement` / `SetAdapterLeakedSlots`, so neither callback is nil in a
running deployment; `applySlotRetryPolicy`'s leaked arm is `pkg/gateway/sessionserver/start.go:2846-2851`
(`slots.MarkLeaked` → `leakGauge` → `health.RecordLeak`); `BindReservedSlot` re-runs
`materializeSlot` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:255`), so the reserved
branch reaches the same discriminator; and the resume branch is CODE-5's third caller. A later
round asking "does the gauge claim hold on the reserved and re-attach paths" can stop here.

FACT: The tier-11 first-match line scan is a real hazard class and I cleared it mechanically
for this staging. `requireLine` (tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:158)
delegates to `lineContaining` (backup_status_enum_test.go:48), which returns the FIRST line
containing the substring. SPEC-3 appends to the `**Scrub model.**` paragraph at spec/05:453,
which precedes every §5.2 bullet a gate anchors on, so an appended substring can shadow the
intended line. Checked each anchor against the staged append text and all are safe:
`"Whole-pod replacement trigger"` (spec/05:561) is capital-W while the append writes lowercase
"whole-pod replacement trigger"; `"Session count limit"`, `"Uptime limit"`,
`"increments \`lenny_gateway_pod_retirement_total\`"`, `"**Fresh-guest reprovision:**"`,
`"the pod is held for its tenant through the claim's \`reserved\` state"`,
`"**Slot (session mode).**"`, `"A service-mode slot is a different thing"` and
`"The gateway triggers the whole-pod scrub"` appear in none of the staged text. On the §6.2
side SPEC-4's `**Pre-\`running\` slot cleanup.**` paragraph lands after the fence and therefore
after the general header, so it is inside `generalBlock` and outside `scopedBlock` in
`per_slot_substate_scope_doc_reconciliation_test.go`; it writes the edge as
`receiving_uploads → slot_cleanup` (single arrow) while the loops match on `──→`, so it
interferes with neither loop. It also does not contain "\`leaked\` slot semantics", so
`TestLeakedSlotCountingLifetimeAgrees_F5231` still resolves to spec/06:160.
EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:78-100,
per_slot_substate_scope_doc_reconciliation_test.go:32-74.

FACT: The shipped tier-3 and tier-10 per-slot-cleanup-outcome gates are green under CODE-1
because both drive a full `StartSession` first, so `live` is true and `ExitedCleanly` reduces to
`closeErr == nil` exactly as today. EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:136-152
(`startAndShutdownSlot` calls `StartSession` then `Shutdown`), tests/tier10_conformance/recycle_scrub_conformance_test.go:376-395
(`TestRecycleSessionScrubLeakedOutcomeConformance` starts `slot-sess` before the failing-close
`Shutdown`). This extends the standing "every shipped adapter `Shutdown` test drives a started
entry" fact past `pkg/adapter` into tiers 3 and 10, which nobody had recorded.

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` cannot be reached by anything this
proposal does: its source of truth is a regex over `pkg/adapter/metrics.go` (:25, :54-58), and
`lenny_adapter_leaked_slots` is registered by the GATEWAY
(pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-226) despite its name. So
SPEC-3 naming that gauge in §5.2 — the first §5.2 mention; §6.2:160 was the only spec site
before — adds no catalog obligation and trips no sweep.

MISTAKE (nearly filed, and here is what kills it): the `sessionserver.go` collaborator doc
comments narrow after CODE-5. `slotReplacement` says the counter increments "when the slot
retry policy drains an unhealthy concurrent-mode pod" (pkg/gateway/sessionserver/sessionserver.go:412-416)
and the `SlotReplacement` option repeats it (:1658-1663); after CODE-5 two of the three
`accountSlotFailure` callers are not the slot retry policy. Real and verifiable, and it dies on
the bar: a Go doc comment is not one of the spec/docs/schemas/charts surfaces criterion (d)
names, the metric's own operator-facing descriptions (spec/16:15,
docs/reference/metrics.md:167) stay true, and nothing an implementor builds changes. Same class
as the five bookkeeping candidates this loop has already refuted. Do not spend a verifier pair
on it. (`pkg/gateway/sessionserver/sessionserver.go` is also absent from `## Files touched on
application (non-spec)`, which lists only `start.go` for that package — that absence is the
same non-finding, not a second one.)

MISTAKE (nearly filed): `lenny_slot_pod_replacement_total` is registered with the single label
`pool` (gatewaymetrics_credential.go:211-216, `IncSlotPodReplacement(pool string)` at
gatewaymetrics.go:1201-1207) while spec/16:15 and docs/reference/metrics.md:167 both state
`pool, k8s_pod_name`. That is a live spec-versus-code divergence, it is PRE-EXISTING, this
proposal opens neither site and makes no claim about the label set, and CODE-5 only adds
callers of the existing emitter. It belongs to whoever audits §16.1, not to 0081.

USEFUL [Settled: "spec/16_observability.md needs no edit and can orphan no alert"] — it named
exactly the four rows to re-check and all four held, which let me spend the round on the gate
sweep instead of re-deriving the inventory.

USEFUL [Settled: "No gate hard-fails on the staged spec text"] — it listed the five gates it
had checked; the four §5.2/§6.2 gates I add above (`concurrent_slot_lifecycle`,
`vm_restart_reprovision`, `slot_definition_glossary`, `session_scrub_report_addressing`) extend
that list rather than duplicating it, and the first-match shadowing reasoning is the part that
was missing.

WATCHOUT: the delta the brief promises is not observable again. `diff -rq` over the whole
`scratchpad/cp-snap/0081` tree shows `non-spec-recheck-r4`, `-r4-start`, `-r5` and `-r5-start`
all byte-identical to the live staging outside the review log; the last real change to any
staged file is `non-spec-recheck-r3` → `-r4`, and it touched `non-spec-changes.md` only. Round 4
recorded the same thing. Do not spend time hunting a fix-stage delta in this lane.
EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r4 versus the proposal directory.


### [non-spec-recheck.5.review-performance.1]

DECISION: returned an empty findings list — BECAUSE every capacity and failure-mode angle I could
quantify either lands inside a budget the spec already states, is already recorded in `### Open`, or
is already on the refuted list. ALTERNATIVES: I built and dropped four candidates, each recorded
below with the arithmetic that killed it, so nobody rebuilds them.

FACT: no division-by-zero in `slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions
int32)`. Every `SlotBindRequest` construction site is gated by `if match.MaxConcurrentSessions > 1`
(pkg/gateway/sessionserver/start.go:2139, :2352, :2478 all guard the single `slotBindRequest` builder
at :2540), and the resume path normalises through `maxConcurrentSessions(bound)` which clamps to 1
(start.go:3353-3358, applied at :4029). So the denominator is >= 1 on every reachable call. A round
tempted to file a panic here should stop; I checked all six sites.

FACT: on an EXCLUSIVE pool reached through `Binder.Resume`, `slotCleanupBudget` degenerates to the
whole pool `cleanupTimeoutSeconds` (max(T/1, 5) == T), so a failed re-attach on a pool configured
with `cleanupTimeoutSeconds: 60` (spec/05_runtime-registry-and-pool-model.md:412) blocks the gateway
request goroutine for up to 60s on a context the caller's cancellation cannot reach
(`context.WithoutCancel`). I did not file it: §5.2's CRD rule only bounds the ratio from below
(`cleanupTimeoutSeconds >= maxConcurrentSessions * 5`, spec/05:545), the proposal claims no upper
bound anywhere (grep for "latency|SLO|blocks" over the three staging files returns nothing), and the
session-creation SLO explicitly excludes this window (spec/16_observability.md:626 scopes P99 < 500ms
to auth/policy/credential-precheck/pod-claim/postgres-persist). UNVERIFIED: whether a resume storm's
aggregate goroutine hold at Tier 3 matters; nobody has priced it.

MISTAKE (nearly filed): "the compensation adds unbounded latency to `/start`". Priced honestly it is
at most `(maxSlotRetries + 1) x budget` and `maxSlotRetries == 1`
(pkg/gateway/sessionserver/start.go:2720), with a 5s floor, so the worst concurrent-pool case is ~10s
on a path the session-creation SLO does not measure. Not a bottleneck at any tier.

MISTAKE (nearly filed): "CODE-5's reserved branch turns a blob-store or bad-image outage into a
pool-wide drain storm at `maxConcurrentSessions: 2`". The arithmetic is real —
`UnhealthyThreshold(2) == 1` (pkg/gateway/runtime/slothealth/slothealth.go:215-220), so every
accounted failure stamps `lenny.dev/drain-request` and requests a replacement, and at Tier 3's 200/s
session-creation rate a correlated fault is 200 drains/s — but the SAME storm already happens today
on the combined path through `applySlotRetryPolicy`'s `RecordFailure` (start.go:2854-2873). CODE-5
makes the two-step path match, which §5.2 states with no carve-out by code path. Conformance, not a
new bottleneck, and the proposal states the consequence in CODE-5's trade paragraph and in the
"Faster pod churn" accepted-failure-mode bullet.

MISTAKE (nearly filed): "the `leaked` occupancy hold has no durable backing, so a Redis reset frees
occupancy the adapter still holds". True — spec/12_storage-architecture.md:191 and :219 both rebuild
`lenny:pod:{id}:active_slots` from `SessionStore.GetActiveSlotsByPod`, which reads rows with
`state='active'`, and a failed bind has no such row. It is pre-existing for every shipped `leaked`
slot and the standing trap already records it with the right remedy (widen §5.2's rehydration text,
not §7.1). Do not re-file.

FACT: `SlotClaimer.ReleaseSlot`'s nil-counter fail-closed check precedes the new `leaked` early
return (pkg/gateway/podlifecycle/podclaim/slotclaimer.go:819-837), so CODE-4's disposition parameter
does not bypass the §12.4 fail-closed gate. I checked this specifically because a `leaked` short
circuit above the nil check would have been a real §12.4 violation.

FACT: `ExcludePod` costs nothing at scale. `ClaimSlot` already does one `c.podClaim` GET per pod in
pass 1 and pass 2 (slotclaimer.go:419, :479); the skip sits beside `expiredByUptime` (:433, :491),
which is AFTER that GET, so the filter adds one string compare per candidate and no API call. It is
also correctly placed before `rebindReservedSlot` (:450), so the filter stays read-only as the
proposal claims. `applySlotRetryPolicy` takes `req` by value (start.go:2807) so the exclusion cannot
leak across requests, and with `maxSlotRetries == 1` there is exactly one iteration it must reach.

FACT: the compensation adds no control-plane write. `slothealth.Tracker` and `slotstate.Registry` are
both in-process maps (slothealth.go:56-66; pkg/sandbox/slotstate/registry.go), `MarkLeaked` and
`RecordLeak` touch no store, and the only etcd write on the path is the pre-existing `DrainSandbox`
annotation stamp. No new watch, no new informer, no new Redis key.

OPEN: the tier-1 gateway cases pin the discriminator arms but nothing at any tier pins that a failed
bind's added `Shutdown` stays inside its budget, and the budget is the only thing bounding the
detached context. Recorded rather than filed because a prior round already refuted the
"`slotCleanupBudget` has no listed test" finding as an extra case inside a tier the proposal already
exercises. If the exclusive-resume 60s figure above ever becomes a concern, that is where the
assertion would go.

USEFUL [Traps: "The leaked-occupancy hold has no durable backing"]: killed a finding I had already
drafted with §12.4 evidence in hand.
USEFUL [Open: "Correlated re-attach churn at Tier 3"]: my own lens' round-1 entry; it stopped me
re-filing the resume-storm aggregate a second time.


### [non-spec-recheck.5.review-reliability.1]

DECISION: filed exactly one finding, the CODE-2 rollback's `releaseSessionSlot(sessionID)` acting on a
successor attempt's entry — BECAUSE it is a race in a mechanism this proposal invents, its harm
(a live retried session's registry entry deleted and its `/workspace/slots/{sessionId}/` tree
`RemoveAll`'d) is severe, and it has a minimal remedy inside the files this loop may edit
(replace the call with a bare `cancelPodMCPIfRuntimeIdle()`, no `slotState` field, no
`pkg/adapter/slot.go`). ALTERNATIVES rejected as below the bar or already refuted: the
leaked-disposition-suppresses-pod-retirement family (refuted three ways); transient
over-assignment on the acknowledged racing-start ordering (verified PRE-EXISTING: today's
gateway already decrements the reservation for a `StartSession` it timed out on while the
adapter completed the start, so the proposal narrows rather than widens it); the compensating
`Shutdown` sitting inside `errors.As(err, &sbe)` while the lease release deliberately sits
outside it (every current stage returns `*SlotBindError`, so it is hardening against a future
stage); `slotCleanupBudget` possibly shorter than the adapter's own 10s socket grace (guarded by
the recorded `cmd != nil` / no-`SpawnPath` trap).

FACT: `releaseSessionSlot` is keyed by session id ALONE and snapshots nothing. `deregisterSlot`
re-reads `s.slots[sessionID]` at call time and `removeSlotTree(st)` uses whatever entry it found.
Since `SlotID == SessionID`, any rollback or lagging teardown that names a session id acts on
whichever attempt currently owns that id. EVIDENCE: pkg/adapter/slotsession.go:174-186,:214-220.

FACT: the rollback's own `Runtime.Close(ctx, sessionID)` sits BETWEEN the guard's read and
`releaseSessionSlot`'s lock, so the successor window is as wide as a runtime close, not a
hairline. On `SocketRuntimeProcess` that close can run the whole SIGTERM→SIGKILL grace.
EVIDENCE: proposal non-spec-changes.md:246-257; pkg/adapter/socketruntime.go:435-467.

FACT: an expired or cancelled ctx does NOT collapse the adapter's shutdown grace to zero.
`resolveShutdownGrace` only uses a deadline when `time.Until(dl) > 0` and otherwise falls back to
the configured/default grace, so CODE-2's rollback close on a cancelled inbound ctx still runs a
normal close. EVIDENCE: pkg/adapter/mcpruntime.go:312-324. This kills the "rollback close is a
no-op on the expired context that triggered the compensation" candidate.

FACT: `adapterclient.Client` applies NO per-RPC timeout of its own; every RPC rides the caller's
ctx. So a `StartSession` that fails on deadline means `applySlotRetryPolicy`'s own
`ReleaseSlotReservation(ctx, …)` and its retry `BindSlot(ctx, …)` are already dead. That is
pre-existing (the leak/`MarkLeaked` arm fires there today) and it is why the compensation's
`context.WithoutCancel` is the only thing that survives that case. EVIDENCE:
pkg/gateway/runtime/adapterclient/client.go:133,:807; pkg/gateway/sessionserver/start.go:2834.

FACT: `ReleaseSlotReservation` has exactly six production call sites plus the `slotBinder`
interface line, and CODE-4's table covers all of them: slotbinder.go:172 (ClaimSlot connect
stage), :217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), start.go:2727 (interface),
:2834 (applySlotRetryPolicy), :3246 (rollbackClaim). Verified by grep; nothing is missed.

FACT: `materializeSlot`'s five error branches each return `b.slotBindError(...)` and each
`cl.Close()`, so CODE-4's wrapper-owns-the-close restructure loses no path and `errors.As` matches
on every current stage. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:284-325.

FACT: CODE-1's claim that "a session the runtime was given and that reaches an ordinary session
end is in `runtimeLive`" holds. Both non-`Shutdown` `noteRuntimeClosed` callers remove the entry
in the same pass: holdstate.go's pass 1 is `deregisterStartedSessions()` and sdkwarm.go:297 is
followed by `releaseSessionSlot`. EVIDENCE: pkg/adapter/holdstate.go:190-195,:251;
pkg/adapter/sdkwarm.go:296-298.

WATCHOUT: the "When it does not fire" bullet (non-spec-changes.md:588-597) names a lagging
`os.RemoveAll` of the RECLAIM as the producer on the acknowledged branch, but an acknowledged
`Shutdown` has already finished `removeSlotTree` before it answers. The real lagging producer on
that branch is CODE-2's rollback, whose lag is the whole `Runtime.Start` duration. A reviewer who
reads that bullet and concludes the hazard is recorded will miss the rollback.
EVIDENCE: non-spec-changes.md:588-597 versus pkg/adapter/session.go:243-282.

UNVERIFIED: whether the adapter workspace-prep handlers can re-insert an entry AFTER an answered
reclaim (`ensureSlotPaths` at the top of `resolvePrepareStagingDir` / `FinalizeWorkspace`). I
re-derived that the top-of-handler insert makes it unlikely for the ENTRY, but a slow
`FinalizeWorkspace` or `RunSetup` still writing into the slot directory after `removeSlotTree`
re-creates directories, leaving a tree with no entry. Judged below the bar (the whole-pod scrub's
`rm -rf /workspace/slots/*` sweeps it and no residue predicate reads it), but nobody has actually
traced the file-writing path. A code-lane reviewer with time should.


### [non-spec-recheck.5.review-security.1]

DECISION: returned an empty findings list — BECAUSE both security checks came back clean against the
staged text and the tree, and every security-shaped candidate I could build is already on the refuted
list, in `### Traps`, or recorded as a pre-existing defect in the summary — ALTERNATIVES: I built and
dropped six candidates, each listed below with what killed it, so nobody rebuilds them.

FACT: the staging has NOT changed since `non-spec-recheck-r4-start`. `diff -rq --exclude='*review-log*'`
against `non-spec-recheck-r4`, `-r4-start` and `-r5` is empty; the last real delta is
`non-spec-recheck-r3-prefix` → now, which is the tier-7a paragraph rewrite alone
(non-spec-changes.md:828-875). This confirms Settled entry "The round-4 snapshot is empty" for a second
round. EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r3-prefix vs the live proposal.

FACT: the 0080 §1.19 impacts row's three membership claims check out against the tree. `boundSlotState`
refuses on `!ok || st.sessionID == ""` (pkg/adapter/slotsession.go:274-283), and CODE-2's rollback calls
`releaseSessionSlot`, which deregisters the entry (slotsession.go:214-220), so a rolled-back start really
does move from the unbound-entry refusal to the absent-entry refusal. Do not re-derive.

FACT: `slotCleanupBudget`'s division cannot divide by zero. `slotReq.MaxConcurrentSessions` is set at
pkg/gateway/sessionserver/start.go:2546 and every route to it is gated on `match.MaxConcurrentSessions > 1`
(:2139, :2351, :2465), and the resume side normalises through `maxConcurrentSessions(...)` at :4029. A
lens eyeing the `cleanupTimeoutSeconds / maxConcurrentSessions` formula for a panic will find none.

MISTAKE (nearly filed, six dresses, each with what killed it):
- "The leaked disposition is sourced from the adapter's `exited_cleanly` self-report, so a pod-side
  report bounds a security property." Killed: `Binder.ReleaseSlot` already keys `leaked` off that field
  on every ordinary session end (pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543). The staged
  lane adds callers of a shipped pattern; on the two paths it newly reaches (reserved branch, §7.3
  re-attach) today's accounting is ZERO, so the change is strictly more accounting, and the adapter can
  only make itself look healthier, which is what it does today by default.
- "`ExcludePod`'s discriminator is an adapter self-report gating a safety constraint." Killed by
  "merely less strict than it could be is NOT a finding": the gateway has no independent measurement,
  and the `relErr != nil` arm is gateway-side and independent.
- "The compensating `Shutdown` at the session-start stage calls `Runtime.Close`, which on
  `InProcessRuntime`/`MCPRuntime` tears down co-tenants." Killed: identical call, identical blast radius
  on every ordinary co-tenanted session end today, and concurrent pools are tenant-pinned so the
  co-tenant is the same tenant. Also `cmd/lenny-adapter/main.go:350-359` selects the socket transport in
  production.
- "A `started` reclaim whose `removeSlotTree` fails leaves `credentials.json` on a pod that keeps
  serving, and `exited_cleanly` is still true." Killed: shipped code is `_ = removeSlotTree(st)` with
  `ExitedCleanly: closeErr == nil`, so the started arm is byte-identical before and after; Trap "Do NOT
  surface `treeErr` on the started path unconditionally" bars the fix.
- "CODE-1 widens `removeSlotTree` to `removed`, so the timer-cancel-then-remove ordering newly strands a
  credential file with its §4.9 enforcement disarmed." Killed: a registered-but-unbound entry has no
  credential file (only `assignCredentialsSlot` writes one), and a bound-but-unstarted entry already
  satisfied the shipped `bound` gate, so `removeSlotTree` already ran for it. No widening. This is
  standing Open #316 and it is genuinely pre-existing.
- "The racing-start ordering leaves a credential file no one reclaims." Killed: `SlotID == SessionID`, so
  the reclaim's `removeSlotTree(st)` deletes the same paths the racing claim recreated; and the recreated
  entry mints no credentials. The residue is a session running on a deleted workspace, which the proposal
  already records as an accepted failure mode.

USEFUL [Traps: "MISTAKE nearly filed twice: the withheld report as a residual-state relaxation"]: this is
the single highest-value entry for a security lens on this proposal. The withheld `ReportSessionScrub`
looks exactly like a relaxation of the `recycle.maxSessionsPerPod` residual-state bound, and it is the
first thing the lens reaches for. The entry's kill argument (today's failed-bind path sends no `Shutdown`
at all, so the counter does not advance there either; the per-slot cleanup the proposal ADDS is what
reclaims the residue; the whole-pod scrub and `maxScrubFailures` remain the backstops) saved a full
verifier pair.

USEFUL [Settled: "`exited_cleanly` is read on the CONCURRENT slot-release path only"] and
[Settled: "The `leaked` disposition already exists end to end"]: together these pre-empt the entire
"pod self-report bounds a security property" family, which is check (2) of this lens applied to this
proposal's one new trust-boundary surface. A security lens that has not read them will spend the round
rebuilding them.

OPEN: nothing new. The security-adjacent standing items (#316 timer-cancel-versus-credential-removal
ordering, #319 whether `maxSessionsPerPod` should count a bind that reached `RunSetup`, and the
summary's Redis-rehydration row) all remain correctly parked as pre-existing and out of scope; I
re-derived each and agree with the parking.


### [non-spec-recheck.5.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE the staging text is byte-identical to the
round-4 snapshot (only the review log moved), the round-4 test-coverage lens already converged on
this exact text, and every behaviour CODE-1..CODE-5 and DOCS-1 change has a listed concrete test at
a tier it reaches, with the non-happy paths (unacknowledged reclaim, cancelled context, upload-free
branch, sole-candidate exhaustion, refused `Shutdown`, exclusive-pool no-op, rolled-back start) named
individually — ALTERNATIVES: filing tier 3 on the `exited_cleanly` semantic widening (no proto/JSONL/
HTTP/CRD surface changes and trap 285 already killed the attribution half); filing the tier-1 resume
accounting case as unplaceable (refuted below); filing `binder_test.go`/the tier-7a directory as
missing file-list entries (bookkeeping, already OPEN 336/340, not test-listing adequacy).

FACT: the r5 snapshot is again empty. `diff -rq scratchpad/cp-snap/0081/non-spec-recheck-r{4,4-start,5,5-start}`
against the live proposal differ only in the review log; the last real staging delta is r3→r4, the
tier-7a paragraph rewrite at non-spec-changes.md:829-872. Settled entry 172 already says this and it
held again. Do not spend a round hunting a delta.

FACT: every citation in the round-4 tier-7a rewrite checks out, verified line by line this round.
EVIDENCE: pkg/adapter/session.go:156 (`Runtime.Start`), :163 (`noteRuntimeStarted`);
pkg/adapter/resume.go:42 (chunks/transport precondition), :101-104 (conversation-only restores
nothing), :140, :144; pkg/adapter/runtimegeneration.go:25-48 (`noteRuntimeStarted`/`Locked`), :50-68
(`noteRuntimeClosed`); tests/tier7a_load_local/podmcp_arming_handoff_test.go:46-101 (`gatedRuntime`,
whose `Start`/`Close` are bare `park` calls holding no conn, child or listener — this is what makes
the "asserts nothing about what the rollback close did" sentence true);
podmcp_once_per_pod_start_race_test.go:232-256 (one rendezvous driving `StartSession` and `Resume`
from one body); pkg/adapter/socketruntime.go:435-446 (the `!p.connected` and sibling-active early
returns); socketruntime_test.go:252 (`TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`).

FACT: the staged tier-11 assertion's anchors are exact. `generalSlotEdges` is
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, the positive loop over the
either-concurrency block is :55, the negative loop over the concurrent-occupancy block is :70, and
`requireAllContain` over the doc's per-slot section is :102-108. DOCS-1's row spelling
`` | `receiving_uploads` | `slot_cleanup` | `` matches the shipped row format at
docs/reference/state-machines.md:234-235, and that table sits inside `### Per-slot sub-states`
(:230-239), which is the section `section(doc, "Per-slot sub-states")` returns. Do not re-derive.

FACT: the tier-1 resume accounting case IS placeable, which closes the mechanical half of OPEN 323.
`s.podBinder` is a concrete `*podsession.Binder` (pkg/gateway/sessionserver/sessionserver.go:188) and
`resumeOnPod` calls its `Resume` directly, so no interface seam exists — but the package's own tests
already construct real `podsession.Binder` values against a fake/envtest client at
start_preclaim_internal_test.go:655,:720,:779, terminal_reclaim_internal_test.go:52 and
start_pod_test.go:203, and start_pod_test.go:1420 already drives `resumeOnPod`. What is still
unverified is only whether one of those fixtures can make `Binder.Resume` fail carrying a
`*SlotBindError`; the shape exists.

WATCHOUT: `applySlotRetryPolicy` is driven through the `slotBinder` interface
(pkg/gateway/sessionserver/start.go:2725-2730) while `resumeOnPod` is not. A reviewer who reads the
`slotBinder` seam and assumes the resume accounting case gets the same fake will file a
feasibility finding that the fixtures above refute. EVIDENCE: pkg/gateway/sessionserver/start.go:2725-2730
versus :4041.

MISTAKE (nearly filed, and here is what kills it): "`Binder.Resume` returning a `*SlotBindError` in
its chain changes `isTransientPodClaimError`'s answer, and nothing tests that it does not."
`isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3681) matches only
`*PoolWarmingError`, `*CredentialAssignmentError`, `*SetupCommandFailure` and five sentinels;
`SlotBindError.Unwrap` returns `Err` (slotfailure.go:75), so every one of those still matches through
the new link. No classification moves and no test is owed.

USEFUL [Settled 152 / 178]: the pair that says tier 7a is an external `_test` package with only
`SoleSessionID()` as an exported cohort read, AND that `SoleSessionID()` still discriminates both
tier-7a variants (residue `{bob}` cohort 1 → "bob" vs correct empty → ""; co-tenanted correct
`{cotenant}` cohort 1 → cotenant id vs residue cohort 2 → ""). Without the second half the rewritten
paragraph's `runtimeLive`/`runtimeIdleLocked` assertions read as unassertable and the already-refuted
"no seam is staged" finding rebuilds itself. Two of the paragraph's four assertion sentences depend on it.

USEFUL [Traps 279, 284, 285, 286]: the four nearly-filed tier items (S7 omits 7a; the `live ||`
disjunct untested; the tier-3 dismissal's attribution; tier 5 reached). Each is a live temptation on
the current text and each carries the reasoning that kills it. A test-coverage lens that reads only
the Testing section will re-derive all four.

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
