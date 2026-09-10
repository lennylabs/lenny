# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 8).** Pass 8 read the whole ledger for this window: the `spec.2`
fix pair and its eleven lens shards, `spec.3` (two fix groups, eight lenses), the `spec.4`
citation shard, `spec.5` (four fix groups, eleven lenses), `spec.6` (three fix groups, three
fix-design groups, ten lenses), `spec.7` (nine lenses), `spec.8` (eleven lenses), the
index-and-checklist reconciliation block, `non-spec-recheck.1.fix-G1`'s correction list, and the
whole `spec.1.fix-bind-epoch` correction chain. Lifted: thirty-seven new Settled entries,
twenty-four new Traps, fifteen new Open items and four new Deferred entries. Applied five CORRECTS
against the standing context. (1) The bind epoch is minted PER REGISTRY ENTRY again: the r5→r6 fix
pass reversed pass 7's per-attempt design, deleted the request-side echo, and recorded the
inheritance hazard as an accepted residue; every entry that assumed a seven-request echo is
rewritten or retired. (2) The `AssignCredentialsResponse` "hard field-number collision" Trap and
its Open were both FALSE and dangerous, corrected by eight independent lenses; acting on them
would have renumbered a correct field. (3) §7.1's retry-exclusion rationale no longer charges a
hold refusal to the §5.2 retry budget. (4) The epoch is defined as owning a slot IDENTIFIER rather
than an occupancy. (5) The adapter-restart epoch-collision question is closed by the podspec's
`RestartPolicy: Never`, not by the no-re-dial rule, which does not carry the case. Retired as
closed: seven Open items (epoch restart, the epochless-`Shutdown` wire spelling, the
`AssignCredentialsResponse` number, the exclusive-pool hold bound, the epoch-reporting RPC set,
the two-start-points hold window, `examples/runtimes/echo/`) and one Deferred (the request-side
wiring, dissolved by the revert). Deleted one Trap outright, the false field-number MISTAKE.
Did NOT reach 691 lines: this section is about 750. The window carried a whole design reversal on
top of unreviewed amendment text, and both the withdrawn per-attempt form and the per-entry form
that replaced it have to stand, because a round that reads only one of them re-derives the other.
`### Traps` is where the length lives and it is the block every lens that reports a USEFUL cites.

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
- **The whole summary structure was rebuilt and verified by the `f5` cleanup firing.** The required sections stand in the required order, `## Open decisions for human to make` carries entries 9 and 11 and only those, `## Defects in the shipped tree that this proposal does not stage` carries eight entries, `## Impacts on other proposals` carries nine rows with no duplicate subject (four for 0080, one each for 0073, 0075, 0078, R1b and R12), and `## Deliverable index` is last. Nothing was relocated because nothing was misplaced.
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
- **Only `ResumeRequest` (field 14) and `ShutdownRequest` (field 6) carry `coordination_generation` among the epoch-relevant messages;** the other six bind-sequence requests carry none and no bind-sequence RESPONSE carries one. So §4.7.1's "Where both appear on one message, as on `Resume` and `Shutdown`" is exhaustive rather than illustrative. Derive it with `awk '/^message /{m=$2} /coordination_generation = /{print NR": "m}'`.
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
- **DECISION: the shared-entry residue is ACCEPTED and unstaged, and NO lettered or numbered decision is minted for it.** The two "open decision D1's Option B" citations were false (no D1 and no Option A/B exists anywhere) and are replaced by a descriptive statement of what closing would cost: a per-ATTEMPT discriminator carried on every request that can create or resolve a slot entry, which this proposal does not stage. Entry 11 is the sole open decision; entry 9 was deleted as resolved.
- **The hold is keyed on the slot identifier, which IS the session identifier,** so N concurrent cleanups on one pod refuse N distinct identifiers and never each other, no other session can collide with a held identifier, and no placement decision is affected. That identifier-scoping is what kills most availability findings against the hold. The proto states the identity too, in `ShutdownRequest`'s `reserved 4` comment (schemas/lenny-adapter.proto:1613-1619) — cite that rather than hunting spec prose.
- **spec/18 still needs no edit after SPEC-5.** Its adapter-conformance anchors are Phase 2/5 (`cmd/lenny-compliance`, the Basic battery over the RUNTIME binary, spec/18:121-122) and Phase 12c (:529-533, the cleanup/scrub split); neither enumerates an admission rule, an RPC field set or a §15.4 obligation, and CONF-1 lands in `tests/tier10_conformance`. Extends the pre-SPEC-5 Settled entry; three lenses derived it.
- **§28 carries no `Shutdown` row and no bind-sequence RPC row,** and §29.2/§29.4 label the bind and teardown RPCs "no register entry, the internal control API". So SCHEMA-1 widening eight messages adds no §28 row and the "deliberately untouched · §28's registers" bullet holds. §28.5.1's gateway-to-pod cards are exactly CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER and CH-PODHEALTH.
- **The adapter proto has no parallel client representation to keep in step.** No SDK, `docs/` or `spec/` file names `ShutdownRequest`, `AssignCredentialsResponse` or any bind-sequence response field, and there is no second copy of `lenny-adapter.proto` outside `schemas/` and `scripts/specshift/testdata/`. SCHEMA-1's nine fields and one enum mirror only `pkg/gen/adapter/v1`, which is generated.
- **`lenny_adapter_leaked_slots` IS named in `spec/` twice** — spec/05:545 and spec/06:160 — so SPEC-3's §5.2 mention mints no new metric surface. This corrects the natural misreading of the Settled entry that records the gauge as "absent from spec/16 and docs/reference/metrics.md", which is true of the inventories and is not a claim that `spec/` never names it.
- **The adapter registers no bind-refusal and no slot-admission metric.** `pkg/adapter/metrics.go:15-90` carries only the SO_PEERCRED pair, the LLM in-flight gauge, four credential-rotation series, the control-event counters and the tracing-frame drop counter, so §5.2's "no report and no counter names it" is true against the tree.
- **`examples/runtimes/echo/` DOES NOT EXIST in the tree.** spec/15:1466 calls it "A Go reference implementation of the adapter" while spec/15:1491 and :1812 describe it as a RUNTIME writing JSONL to stdout, and `examples/` is absent entirely. Pre-existing spec fiction; it cannot be an unstaged edit site, which moots the standing Open.
- **The gateway's `/finalize` block has a window in which §7.1's obligation cannot be discharged as written.** Its two "Gap 2" branches — the `finalizing → ready` store Update (sessionserver.go:3167-3181) and the upload-token `ConsumeDigest` (:3188-3197) — fail AFTER `Binder.Prepare` succeeded and CLOSED its connection, and both call `reclaimFinalizedPod` → `ReclaimClaimed`, which deletes the claim and revokes the lease and sends the adapter NO RPC. Staged §7.1 names the creation finalize block, mandates the reclaim "on the connection the failed stage still holds", and forbids re-dialling. FILED.
- **`isTransientPodClaimError` is a closed enumeration of sentinel types** (start.go:3648-3682) that a bare `codes.Aborted` matches on none of, so a §7.3 resume refused by the hold falls through to `s.failSession` and takes the row terminal.
- **The tier-11 §4.7 RPC-row gate harvests backticked first-column table rows.** `spec_47_rpc_row_naming_test.go:32-53` reads §4.7 with `specSection` and holds every `§4.7 table names <RPC>` Go comment to the row set it finds. SPEC-5 inserts prose rather than a table, so it is green; any future §4.7 or §4.7.1 edit that adds a markdown table with a backticked first column silently widens the accepted set.

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
- **`compensateFailedSlotBind`'s signature on the resume path** — OPEN: it is staged as `req SlotBindRequest` while `Binder.Resume` holds a `ResumeRequest`. The proposal solved the identical mismatch for `accountSlotFailure` by taking scalars and did not carry it across; `ResumeRequest` does carry `SessionID`, `MaxConcurrentSessions` and `CleanupTimeoutSeconds`, so the scalar remedy works. Five lenses recorded it and none filed it; a fixer must not silently invent a request conversion. See `non-spec-recheck.1.review-applicability.1`.
- **SPEC-3's exclusive-pod arm over-reaches its antecedent** — OPEN: the clause's subject is a bind "abandoned or fails" while §7.1's paragraph binds only "a gateway bind attempt that fails". A READY_TIMEOUT or a revoke retires the pod anyway under spec/05:455; the surviving class is a client terminate at `ready` on a recycling exclusive pool, where the session ends `completed` and the pod is reused. Open decision 11 owns the trigger noun-phrase. See `spec-recheck.3.review-edit-sites.1` and `spec-recheck.3.review-applicability.1`.
- **What disposition the exclusive-pod abandonment arm should name** — UNVERIFIED: §5.2 scrub step 6 marks the scrub failed if `/workspace/slots/` or `/run/lenny/slots/` is non-empty, which looks like the right pointer, but nobody has checked whether a half-completed cleanup's tree trips step 6 or is swept by steps 1-6 first. See `spec-recheck.3.review-applicability.1`.
- **Does §4.6.1 orphan GC reclaim a resume-path leaked slot?** — UNVERIFIED: it drains a `bound` claim whose pod no active session references, so it fires only while the pod carries no other session; once `ClaimSlot` pass 1 places a same-tenant session there the claim is referenced and the leak has no reclaim route. Check `claimOrphanTimeout`'s predicate against a pod at occupancy 1 with one live sibling. See `spec-recheck.1.review-performance.1`.
- **Does any gateway path patch `SandboxClaim.status.phase` to `failed`?** — UNVERIFIED: only a bare DELETE was found on the pre-attached bind-failure path, so the terminal disposition the §4.6.1 projection names may not exist in code. Check the claim-status patch callers before the code lane assumes it. See `spec-recheck.3.review-kubernetes.1`.
- **Does the resume-path slot failure owe an `error_type` value?** — OPEN: `lenny_slot_failure_total` has no series on that path, so a §7.3 re-attach can move the leak gauge and trip the threshold with no slot-failure signal. A new label value mints no §16 or metrics-reference edit, because both rows state the label without enumerating its values. Either answer is legal and the proposal states neither. See `non-spec-recheck.1.review-operational.1`.
- **The tier-7a park placement, and what the co-tenanted variant asserts** — OPEN: where the park sits relative to `SocketRuntimeProcess.Start`'s `accept` decides which of the two closes tears the process down, and the paragraph deliberately asserts nothing about it. The variant now names the pod-level cohort outcome instead. A later round either names a teardown assertion, which needs the placement settled and a real `SocketRuntimeProcess` behind the gate, or drops the variant. See `non-spec-recheck.3.fix-G1.1` and `non-spec-recheck.3.fix-design-G1.1`.
- **Is tier 8 reached?** — UNVERIFIED and declined by three rounds: the change is a failure and recovery path in the plain sense, but the tier-8 examples in `.claude/rules/test-coverage.md` are all infrastructure failure injection and the adapter-refusal and blob-outage cases are covered at tier 4. See `non-spec-recheck.3.review-test-coverage.1`.
- **`tests/spec-map.json`** — OPEN, and the ground CHANGED: `validate-maps` is a tier-0 gate that also requires a spec-map entry for every `_test.go` at tier 2 and above, so a NEW file under `tests/tier7a_load_local/` turns tier 0 red while a new test function in a listed file costs nothing. The earlier decline rested on "existence, version and dangling paths only", which was incomplete. 0081 names the file nowhere and sibling 0079 lists it conditionally. Settle this together with the Open below on the tier-7a case naming no file. See `non-spec-recheck-2.1.review-applicability.1`.
- **`binder_test.go` is in no file list** — OPEN: it holds `fakeAssigner` and the `Binder.Resume` fixtures the staged resume case cites, and appears in neither the gateway-tests list nor "Files touched on application". Same package as `slotbinder_test.go`, so nothing fails to compile. See `non-spec-recheck.3.review-applicability.1`.
- **The Decisions bullet names only two adapter files** — UNVERIFIED: summary.md's decision bullet says the adapter edits land in `session.go` and `runtimegeneration.go`, which the CODE-2 extension made incomplete (`resume.go` takes a rollback and `sdkwarm.go` a call-site change). Its load-bearing half, that `slotsession.go` takes doc comments only and `slot.go` is untouched, still holds. See `non-spec-recheck.2.review-fresh.1`.
- **Does a §15.1 retry reach a pod already stamped for drain?** — UNVERIFIED: nothing in `bindReservedSlot` reads `Status.Phase`, so a retry pinned to `row.PodAssignment` may dial a draining pod, succeed and then die with it. The answer decides whether the withdrawn reserved-branch composite is an accepted limitation or a stuck-session bug. See `spec-recheck.3.review-performance.1`.
- **The `**Recycle lifecycle**` paragraph is in no edit list** — OPEN: spec/05:455 is the surface that governs a one-session pod that does NOT retire, and its only retire rule is the failure-or-crash one, which a `completed` terminate does not trigger. Any future sentence asserting "the pod retires" for an exclusive pod has to survive it. See `spec-recheck.3.review-edit-sites.1`.
- **The tier-7a case names no target file** — OPEN, and no longer free: `## Files touched on application (non-spec)` ends at the directory `tests/tier7a_load_local/`, where every other test surface in that list is a file, and under the corrected `validate-maps` reading a NEW file there fails tier 0 unless `tests/spec-map.json` gains an entry. The fix is one filename plus one map entry, not a test. See `non-spec-recheck.4.review-test-coverage.1` and `non-spec-recheck-2.1.review-applicability.1`.
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
- **S1 lands spec text that cites a §4.7.1 statement S5 lands later** — OPEN, deliberately not filed: the only remedy is a checklist reorder, which the spec loop's scope bars. See `spec.1.review-applicability.1`.
- **Is a second `POST /v1/sessions/{id}/start` for the same session admitted while the first is in flight?** — UNVERIFIED: it decides how wide the unfenced first-RPC window is. The finding was reached on the budget-expiry route instead. A code-lane reviewer with the `/start` precondition table open should confirm. See `spec.1.review-performance.1`.
- **Can the gateway placing a §5.2 retry actually see that a pod holds an incomplete reclaim?** — UNVERIFIED: the staged `**Max retries:**` sentence assumes it can and the `ExcludePods` threading is where it is answered; no spec-lane lens traced it. See `spec.1.review-feasibility.1`.
- **Does §15.4 state what the unconditional (no-epoch) `Shutdown` reports on its response?** — OPEN: §4.7's row does (`reclaimed` / `absent`) and the gateway reads one field on every answer, but §15.4 scopes its whole outcome paragraph to "the comparison's outcome" after saying the no-epoch form "is compared against nothing". A third-party adapter author working from §15.4 alone has to derive it. See `spec.1.review-citations.1` and `spec.1.review-client-surface.1`.
- **The `/finalize` Gap-2 window cannot discharge §7.1's reclaim obligation** — OPEN, FILED: both branches fail after `Binder.Prepare` closed its connection and call `ReclaimClaimed`, which sends the adapter no RPC, while §7.1 names the creation finalize block, mandates the reclaim on the connection the failed stage still holds, and forbids re-dialling. The fixer must pick one of two legal answers and make §7.1, §4.7.1's caller rule and the accepted-failure-mode list agree: relax the no-re-dial rule for the epochless form, or record the window as an accepted residue. Keep the finding on the CONNECTION, never on the pod disposition. See `spec.7.review-mechanism.8`.
- **§4.7.1's caller rule mixes per-connection and per-session granularity** — OPEN, FILED: the holding rule defines one per-connection value ("the most recent epoch a response reported to it on one adapter connection") while the naming rule two sentences later reads it per session ("the epoch the caller holds for that session"). Newest, least-examined block. See `spec.8.review-fresh.1`.
- **Does the hold survive its own timeout?** — OPEN, FILED: §5.2's `**Slot cleanup:**` bullet says a cleanup that fails leaves the slot `leaked` and "not reclaimed until pod termination", so the hold's terminal ("until the cleanup that reclaims it has finished") never occurs, yet both staged blocks say the timeout "bounds the hold". A human may prefer to answer by making the hold deliberately permanent on a leaked slot and deleting the two bound sentences. See `spec.2.review-mechanism.1`.
- **Does the §7.1 compensation need an explicit exemption from the §10.1 generation fence?** — OPEN: spec/10:30 obliges a pod to validate the generation on every gateway→pod RPC, `ShutdownRequest` carries it, and SPEC-5 now affirms it is checked on `Shutdown`, so a compensation from a replica whose lease lapsed is refusable and the obligation cannot be discharged. The shipped adapter does not enforce it and the rule predates the proposal. See `spec.8.review-performance.1` and `spec.8.review-security.1`.
- **Does CODE-4's `*SlotBindError` fold need a matching `isTransientPodClaimError` arm?** — OPEN: a client-driven §7.3 resume refused by the hold with `ABORTED` matches none of that closed sentinel enumeration, so it falls through to `s.failSession` and takes the row terminal, contradicting "the refusal is transient, so the attempt keeps its §5.2 retryability" for that one caller. Reachability is doubtful and any remedy is a CODE deliverable. See `spec.8.review-client-surface.1` and `spec.6.review-feasibility.1`.
- **Does §5.2's hold obligation reach the sixteen non-`Shutdown` release sites?** — UNVERIFIED: the staged hold is written as an obligation on every adapter deregistration-plus-cleanup, while CODE-6 takes it only inside `Shutdown`; `releaseSessionSlot` runs at session.go:133,:147,:157, resume.go:69-141 and sdkwarm.go:236,:241,:251,:298 with `s.mu` released between the deregister and the tree removal. Declined twice on the "the action list is ahead of the code; pre-existing" precedent, and it is decidable in the spec lane by scoping the hold's antecedent to the `Shutdown` slot release. See `spec.3.review-docs-alignment.4` and `spec.5.review-reliability.2`.
- **Does a `superseded` successor running on the predecessor's tree want a sentence?** — OPEN: rule 1 admits an epochless successor onto a surviving unstarted entry and `ensureSlotStateLocked` returns that same `*slotState` with its paths, credential map and on-disk tree, so §5.2's "Fresh workspace guarantee" (which assumes a distinct slot) does not hold there. Nobody has asked whether the applied spec should say so, or whether it is the same-session same-tenant retry semantics the create-time-reserved path already has. See `spec.5.review-mechanism.6`.
- **Does the successor that adopts a surviving entry inherit its still-armed §4.9 timers?** — UNVERIFIED: no cleanup and no `deregisterSlotLocked` runs on adoption. Same session and same tenant, so not an isolation issue, but a stale timer firing `AUTH_EXPIRED` at the successor is a correctness question for the code lane. See `spec.5.review-security.1`.
- **Does the hold's predicate want narrowing to the bind sequence?** — OPEN: §5.2 and §15.4 both refuse "any request that would create or resolve a registry entry" with a normative `ABORTED`, which is wider than the seven bind-sequence RPCs the design means. Declined four times on the vacuous-resolve reading. If a round wants it, the correction is to narrow both predicates to the enumerated seven, and the argument has to start from §4.7.2's Checkpoint/Interrupt operation lock, which QUEUES rather than refuses. See `spec.8.review-edit-sites.1`.
- **Does a `Shutdown` carrying neither a graceful window nor a deadline leave the hold unbounded?** — UNVERIFIED: §5.2 names two bounds for a request-borne hold and none for that case; `resolveShutdownGrace` then falls back to the runtime-configured grace or the package default, so the hold is bounded in the tree by a figure the spec does not name. See `spec.8.review-reliability.1` (pkg/adapter/mcpruntime.go:311-324).
- **§15.4's "reports zero on a bind-sequence response does not conform" versus an empty `PrepareWorkspace` stream** — OPEN: a call with zero frames resolves no entry and would report zero. Unreachable today, because `stageWorkspace` sends the RPC only when the plan carries uploads. The fix is to scope the clause to a call that resolved an entry. See `spec.7.review-edit-sites.1`.
- **Would a caller multiplexing two sessions on one adapter connection name the wrong session's epoch?** — UNVERIFIED: the shipped caller cannot reach it (a fresh dial per bind), and the spec never states the one-session-per-connection premise the per-connection latch rests on. A lens owning the caller contract should decide whether §4.7.1 must state it. See `spec.6.review-applicability.1`.
- **Does §28.3's `LNK-POD-GRPC` multiplicity reach unregistered gateway→adapter RPCs?** — UNVERIFIED: that row says "One connection per gateway replica per pod" while §4.7.1 states a bind attempt may span two connections and production dials a fresh client per connect. §29.2 and §29.4 place the bind and teardown RPCs outside the §28 register, which is why three rounds declined it. A channel-naming lens should settle it. See `spec.6.review-edit-sites.1` and `spec.8.review-fresh.1`.
- **spec/04:672's `StartSession` row states no refusal** — OPEN, pre-existing: `claimSessionSlotUnderLock` refuses a repeat start onto a started session with `codes.Unavailable`, and this proposal depends on that refusal twice. Worth its own finding in a later round; do not repair it inside 0081. See `spec.6.fix-design-G1.1`.
- **Retire the Kubernetes lens for the remaining spec rounds?** — OPEN: it has returned nothing on the spec staging in rounds 3, 5, 6, 7 and 8 against text that changed each round only on the gRPC surface. If a future amendment touches no CRD, status write, finalizer, webhook or reconcile loop, retiring it costs nothing and saves a round's latency. See `spec.8.review-kubernetes.1`.
- **Are the two shared-entry edge-case bullets one ordering stated twice?** — UNVERIFIED: `spec.6.fix-design-G3.1` deliberately did not restructure the section, and the two bullets describe near-identical orderings. If a later round merges them, apply the closing-cost sentence ONCE.

### Deferred

- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S10 (line 24) says "One `accountSlotFailure` helper serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold". That is now incomplete, and the fix that landed in the index-and-checklist reconciliation pass corrected a different half of the same line. What is true: the helper serves every bind path the §7.1 obligation binds — `applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch, and `resumeOnPod`'s `podBinder.Resume` failure branch — so the create-time reserved path AND the §7.3 checkpoint-restore re-attach reach the §5.2 threshold. The helper's request-derived parameters also narrow from `req podsession.SlotBindRequest` to `pool string, maxConcurrentSessions int32`, which changes both existing call sites. Three lenses filed this after the third caller was added.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S9 (line 22) describes CODE-4 as carrying the outcome "as the `leaked` disposition through `ReleaseSlotReservation`". Still true and now incomplete: `Binder.Resume`'s failure branch also returns its error with a `*SlotBindError` in the chain (pod, reserved slot id, stage `resume`, and the compensation's `Leaked`), wrapped inside today's `fmt.Errorf("podsession: resume session on pod %s: %w", ...)` message, which is what carries the re-attach's disposition to S10's third caller. An implementor landing S9 from the checklist alone would leave S10 with nothing to read. Whoever fixes S10 should look at S9 in the same edit.
- DEFERRED [proposals/0081_.../0081_....summary.md, deliverable index CODE-4]: the line reads "releases the session's credential leases" unqualified, for "every post-connection bind failure and at a failed `Resume`". After the fix that moved `b.releaseCredentials` out of `compensateFailedSlotBind` and into the `materializeSlot` wrapper, the resume path releases no lease. The same unqualified wording sits at non-spec-changes.md:25 and :319. All three want the resume carve-out, not just the function body.
- DEFERRED [proposals/0081_.../0081_....summary.md, "Watch out for"]: it reads "`TestValidTransitions_spec_6_2` asserts an exact edge count and fatals on a length mismatch. The §6.2 edit and the `slotstate` edit must land together." Both halves are wrong as an instruction. The test compares a `want` list in `pkg/sandbox/slotstate/slotstate_test.go` against `ValidTransitions()`, both changed by CODE-3 alone, so the spec edit is irrelevant to it; and the checklist puts SPEC-4 in the spec lane at S4 and CODE-3 in the code lane at S6, which the one-lane-per-step rule and the spec write lease make mandatory. What is true instead: `TestValidTransitions_spec_6_2` and `ValidTransitions()` must change in the same step, which is S6, and nothing compares either against spec/06.
- DEFERRED [docs/reference/adapter-contract.md]: line 75's `Shutdown` row states the shipped one-teardown contract, "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`". After SPEC-1 and SPEC-3 that is false in three ways: the slot release now runs for an unbound entry, the runtime teardown and the usage flush run only for a started session, and a pre-start reclaim reports no outcome. The row also needs the no-op clean-exit answer for a session the adapter holds no entry for. Secondary, same cause: `docs/reference/adapter-contract.md:81` ("at each session release ... The gateway increments the pod's served-session count"), `docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68`, and `docs/operator-guide/multi-tenancy.md:72` each say the per-slot cleanup runs "at each session release" and reports its outcome, which SPEC-3 widens (cleanup also runs on an abandoned bind) and narrows (that one reports nothing). The only staged DOCS deliverable is DOCS-1 for `docs/reference/state-machines.md`; `adapter-contract.md` appears in no edit list and the file is named nowhere in the proposal outside this log. CORRECTED: the earlier form of this entry said "tier 11 reconciles them", and half of that is wrong. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only that the row contains "end-of-session teardown", "recycle disposition", "ReportSessionScrub" and "ReportPodScrub", every one of which survives SPEC-1, so the false row passes silently and only a reviewer can catch it. Waiting for the gate would have shipped it. Only the ownership half was right: the non-spec loop owns the edit, and it wants a second DOCS deliverable against `docs/reference/adapter-contract.md`. Scope it to :75. The four secondary sites named above all read "at each session release", which SPEC-3 widens rather than falsifies, so they are NOT edit sites; this reconciles the two standing entries that pulled opposite ways on :81. Eleven separate lenses have now filed the :75 site independently.
- DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited") and the `ReportSessionScrub` / `SessionScrubOutcome` comments ("on every session release") are the proto-side statement of the contract §15.4 says is "kept in sync with the prose". SPEC-1's two-teardown split, its no-op clean-exit answer, and SPEC-3's withheld report all falsify parts of them. Programme rule S-2 bars this proposal from opening that file, so the correction belongs to the step that owns it, R1b. The `Shutdown` comment is already stale for a co-tenanted pod, so this is a widening rather than a new break. NARROWED: `ShutdownResponse` carries no doc comment at all (:1665-1668 is a bare three-line message), so CODE-1's re-keying of `exited_cleanly` owes nothing here and the entry is the three comments named below and no more. EVIDENCE: schemas/lenny-adapter.proto:203-206,:308-311,:436-438; spec/15:1456.
- DEFERRED [pkg/adapter/session.go, pkg/adapter/resume.go, a later proposal]: the shipped pre-`Runtime.Start` failure branches release the slot by session identifier alone (session.go:133,:147,:157; resume.go:69,:73,:89,:107,:126,:134,:141; the same pattern also stands at sdkwarm.go:236,:241,:251,:298). Under `SlotID == SessionID` a lagging one deletes a later attempt's entry and `RemoveAll`s the tree, uploads and credential directory that attempt staged, exactly as CODE-2's withdrawn rollback release would have. The claim that "`releaseSessionSlot` is correct against an entry a concurrent reclaim removed" is false for all of them. This proposal does not open those branches; the `f3` firing landed the record as a summary out-of-scope row, and the class fix (an identity-checked deregister threading the `*slotState` the claim returned into `deregisterSlotLocked`) wants its own problem statement.
- DEFERRED [proposals/0081_.../0081_....review-log-archive.md, the WATCHOUT at :3785]: it carries the same rule as the standing Trap "Do NOT add a bind-path or a start-state qualifier to §5.2's leak-accounting sentence" and cites its evidence as "summary.md, the `bindConcurrentSlot`'s-reserved-branch bullet under the recorded limits", which the `f4` firing deleted and which no longer resolves. Re-point it to CODE-5's `accountSlotFailure` doc comment (non-spec-changes.md:515) and its caller list (:537-569), which state the same no-carve-out argument. The standing-context half of this correction is applied; the archive is outside every current lane's editable set.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, the edge-case bullet at :109-111]: it reads "A retry the §5.2 slot retry policy places goes to a different pod; when that pod is the pool's only candidate the retry meets the shipped `WARM_POOL_EXHAUSTED` outcome." The nearest antecedent for "that pod" is the DIFFERENT pod, which makes the sentence backwards, since a retry whose target pod is the only candidate succeeds. The intended subject is the excluded, residue-bearing pod, which the §5.2 rationale states correctly at :406-408. Unapplied prose, so the applied spec is not wrong; a fixer touching that bullet should repair the pronoun rather than reword the rule.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, the `Client.Shutdown` doc comment at :797-798]: it says "A zero deadline lets the adapter apply its default grace period". That is false. When the caller's ctx carries a deadline, `resolveShutdownGrace` takes the ctx's remaining time in preference to both the runtime-configured grace and the package default, so a zero deadline yields the CALLER's own remaining budget; the adapter's default applies only when the caller set no deadline at all. `adapterclient` is not a target of this proposal and the correction cannot land here. Whoever next opens that file should land the one-sentence repair. Derived independently by two shards this window.
- DEFERRED [proposals/0081_.../0081_....summary.md, open decision 9 at :258-260]: it reads "the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's teardown". That was true when the compensating `Shutdown` sent `deadlineMs: 0`. After the `budget/2` split, half the budget IS pinned as the adapter's graceful window and bounds the runtime close, so the sentence now under-describes the call. What is still true, and what carries decision 9's whole hazard, is that nothing bounds `removeSlotTree`: it runs after the close, outside `s.mu`, and the handler performs no context-expiry check, so a lagging reclaim can still be deleting the tree when a retry lands. The accurate clause names the tree removal rather than "the adapter's teardown". Four lenses derived this and none filed it, because a lens may not file on how an open decision is framed and the decision's conclusion is unaffected. A fixer editing that entry for any other reason should tighten it.
- DEFERRED [spec/06_warm-pod-model.md:156 and docs/reference/state-machines.md:236]: the `slot_cleanup ──→ released` annotation and its docs mirror gloss three actions ("slot workspace removed, processes killed, slot released"), a word-for-word mirror of §5.2's shipped three-action list, while SPEC-3 widens that list to five (the credential directory and the §4.9 direct-mode expiry timers join it). Neither is false, because an abbreviated gloss states nothing wrong, which is the standing trap's reasoning for the identical §6.2:148 annotation, but a reader comparing either to §5.2 after this lands sees a shorter list. If DOCS-1 is ever widened beyond its single added row, this is the sibling row to widen with it.
- DEFERRED [docs/reference/error-catalog.md]: nothing, recorded deliberately. Under the placement-rule design, lines 129, 155 and 156 stay TRUE, because no failure class becomes non-retryable. Recorded explicitly so a later round does not re-file them: they are sites only under the rejected no-retry alternative.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md "Spec files touched"]: the spec/07 half is CLOSED, and the line now reads "the section preamble's premise sentence deleted, a sentence added to step 2, and step 3 replaced". The spec/04 half stands: the list still describes the §4.7 row edit as "(first sentence plus one sentence)" when the replacement swaps two sentences for seven or eight, and the lead-in "Replace the `Shutdown` row's first sentence" is stale in the same way. Six lenses re-derived it and none filed it, because the operative quote-and-replacement pair is exact and no implementor can be misled; one lens argued the parenthetical describes the edit rather than the anchor block and is therefore defensible. A fixer touching the SPEC-1 §4.7 block should correct it in the same edit. A count in that list is a trap that re-arms on every later edit; prefer a named set, as the SPEC-3 entry now uses ("the per-slot cleanup pointer and the cleanup-outcome report rules appended"). The §5.2 entry now also understates: a pod-disposition claim lives in that append and the named set does not hint at it.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :63-66]: "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" has no antecedent for "neither". Round 2 named two placement mechanisms, round 3 deleted the second from the surrounding text, and round 4 did not restore it, so only §5.2's slot retry policy is named. What is true: two mechanisms place a further attempt at the same session, §5.2's slot retry policy and the §7.3 re-attach's whole-pod idle claim through `podclaim.Claimer.Claim`, which cannot select a pod whose occupancy the leaked slot holds; the §15.1 create-time-reserved start is placed by neither. Repair the sentence with that fact rather than by deleting "neither".
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :43-44]: the pointer list ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing") omits the SPEC-2 §7.2 snapshot-close edits entirely, although "Spec files touched" carries them. §7.2 step 3 also restates the reclaim's ordering against the pod release, so the sentence is incomplete rather than wrong. A fixer touching the Design paragraph should fold §7.2 in.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge case "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot"]: the round-4 rewrite says "When the adapter does not acknowledge the reclaim … the adapter's entry survives in whatever state the failed stage left it." That is false for the delivered-but-unanswered case: a `Shutdown` the adapter executed while the response was lost removes the entry, and the gateway still classifies the reclaim as unacknowledged (`err != nil || !cleanly`). What is true instead: an unacknowledged reclaim leaves the gateway unable to tell whether the entry survives, so the retry may find either a surviving entry or none. The later sentences of the same bullet already reason about a lagging reclaim, so the correction is one clause.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, CODE-6's `adapterclient` latch]: §4.7.1's caller rule makes `DemoteSDK` the act that drops the caller's held epoch ("holds none once it has itself issued an RPC that removes the entry that epoch names"), and the entry removal is real in the tree (pkg/adapter/sdkwarm.go:296-298). What is true instead of the set-only latch the deliverable describes: `Client.BindEpoch` must answer zero after a successful `DemoteSDK` on a connection that had latched a non-zero epoch, and the latch's lifetime is the SESSION's rather than the attempt's, because `BindResult.Adapter` is retained and closed when the session ends. A tier-1 case asserting the clear belongs beside the existing latch cases; it is not tier 3 and not tier 10, because no shipped call graph produces the ordering over a real transport and CONF-1 publishes adapter obligations, of which this is not one.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md, five steps naming rules this round retired]: S5 (`:14`) names "the published bind-epoch contract and the slot-identifier occupancy contract", where §15.4's second block is now `**Slot-identifier reclaim hold:**` (spec-changes.md:662). S9 (`:22`) says `ensureSlotStateLocked` "gains the hold refusal and the epoch mint", where it now also compares the caller's epoch (non-spec-changes.md:922-924). S11 (`:26`) enumerates the two orderings CODE-2 was rewritten away from: the admission rules refuse a superseded attempt and CODE-2's confirmation now covers only the window between the claim and the runtime record (non-spec-changes.md:42-47, summary.md:58-62). S13 (`:30`) gives the exclusion trigger as an unacknowledged reclaim, which the round broadened to "did not complete" everywhere the rule is stated. S14 (`:32`) names two CONF-1 properties the rewrite replaced with "minted per admitted bind attempt" and the echo and refusal arms (non-spec-changes.md:979-982). The checklist is the artifact an implementor executes, so each step must be brought to the round's text before any step is run. CORRECTED: S8's proto edit reads "one enum and nine fields", and after the r6 revert to a per-entry epoch with no request-side field, nine is RIGHT. Do not raise it to sixteen. S14's "minted per entry" is also right again, and S11's two orderings and S9's caller-epoch comparison need re-reading against the reverted text rather than against the r5 form this entry was written for.
- DEFERRED [proposals/0081_.../0081_....summary.md, the open-decisions preamble]: it says "Entry 9, the create-time-reserved retry's unbounded exposure, is resolved and has left this section: the bind epoch and the reclaim hold close it." After the r6 revert to a per-entry epoch that over-claims: the staged edge cases now state that the same path can tear down a running retried session and call it an accepted failure mode. What is true instead: the epoch and the hold close the released-entry ordering, and the shared-entry ordering is accepted and recorded with what closing it would cost. summary.md:464 and :466 also cite "open decision 9" in the PRESENT tense for the same gap while :325-328 says entry 9 is deleted; both must move in one edit. summary.md:104's past-tense closure prose in `## Decisions` is correct as written.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, the design narrative's mirrors of two deleted spec sentences]: the round deleted "This is the only rule under which the adapter refuses a request on the bind path" from §5.2 and the Design, and re-worded §7.1's "spends one of the attempts the retry policy allows". Any mirroring sentence in the non-spec lane's design narrative goes with them. Also in the same file: the three per-caller bound sentences that follow non-spec-changes.md:131 state the bound as covering the directory removal, which the corrected §5.2 denies; :128-131 and :890-894 already state it correctly ("plus the removal of the slot's directories") and must not be rewritten.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, CONF-1 and the tier-3 list]: CONF-1's properties were written against the r5 per-attempt form. Under the reverted per-entry design the echo property and the mid-session admission case it wanted are dissolved, and what CONF-1 must publish instead is the per-entry mint, the three `Shutdown` outcomes, the epoch comparison and the reclaim hold's `ABORTED` refusal. Whoever next opens CONF-1 must reconcile it with the reverted §15.4 rather than with the standing per-attempt wording.
- DEFERRED [docs/reference/adapter-contract.md:64, widening the standing :75 entry]: after SPEC-5 lands, the `DemoteSDK` row ("Tear down the pre-connected SDK process and return the pod to pod-warm state") is incomplete against §4.7.1's caller rule, which makes the demotion the act that drops the entry and forces the next bind sequence epochless. The same file's §15.4 mirror carries neither the bind-epoch contract nor the reclaim hold. A second DOCS deliverable against `adapter-contract.md` should cover :64 alongside :75.
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

## Ledger

### [spec.2.fix-G1.1]

FACT: Of the seven bind-sequence requests only `ResumeRequest` declares `coordination_generation`; no response message in the whole file declares it, and `ShutdownRequest` does. So `Resume` and `Shutdown` are the only messages where a bind epoch and a coordination generation could co-travel. EVIDENCE: schemas/lenny-adapter.proto:1405,1635; `awk '/^message /{m=$2} /coordination_generation = /{print NR": "m}' schemas/lenny-adapter.proto` returns request messages only.
MISTAKE: Round 1's SPEC-5 block asserted the epoch and `coordination_generation` "travel on the same messages", describing a field set the shipped proto does not have, on the section that is the normative gateway-adapter contract.
MISTAKE: Round 1 closed a too-wide-predicate finding by adding a universal exhaustiveness clause ("Every other RPC on this contract neither carries nor reports a bind epoch") to BOTH SPEC-5 blocks, which excluded `Shutdown` and so contradicted SPEC-1's §4.7 row, §4.7.1's own caller rules, §15.4's comparison paragraph, and `ShutdownRequest.expected_bind_epoch = 7`. The §15.4 copy is the one published to third-party adapter authors, so an author following it would have ignored the fence and torn down a successor's session.
DECISION: Both exhaustiveness sentences now carve `Shutdown` in explicitly and keep the narrowed universal — BECAUSE the closed enumeration is what keeps `Attach`, `Interrupt`, `RotateCredentials` and the rest outside the fence — ALTERNATIVES: widening the bind-sequence list to include `Shutdown` (rejected: the bind-sequence admission rules carry a mint-on-epochless arm `Shutdown` must never have); deleting the sentence (rejected: re-opens the finding it closed); fixing §4.7.1 only (rejected: §15.4 states its own conformance obligations).
WATCHOUT: In BOTH blocks the admission-rules paragraph sits BELOW the RPC-set paragraph. The finding's suggested text said "the two rules above" and was wrong in both places; the landed text says "below". EVIDENCE: spec-changes.md, SPEC-5 §4.7.1 block paragraphs 2 then 3, and the §15.4 block likewise.
WATCHOUT: The §4.7.1 carve-in cross-references "the caller rules below"; the §15.4 copy must not, because §15.4 carries no caller rules. Do not re-synchronise the two sentences word for word.
DECISION: The `DemoteSDK` epoch drop is stated as part of what a caller HOLDS, in the §4.7.1 caller-rules paragraph, and §15.4 is untouched — BECAUSE §15.4 states adapter obligations only and the adapter side is unchanged (after a demotion it holds no entry, so the existing epochless-admission rule mints a fresh epoch) — ALTERNATIVES: relaxing adapter rule 2 to admit an epoch-bearing request when no entry is held (rejected: lets a straggler from a dead attempt re-create an entry and a workspace tree with nothing left to reclaim it, which is this proposal's own title); deleting "and it names no other" (rejected: converts a specified rule into silence on the normative contract); leaving SPEC-5 as it was because the shipped gateway never continues a bind sequence after a demotion (rejected: the contradiction is between two pieces of specification text and spec/04's `ConfigureWorkspace` row is the one this proposal does not touch).
FACT: spec/04_system-components.md:673 makes a failed `ConfigureWorkspace` call `DemoteSDK` and fall back to pod-warm materialization, and spec/15_external-api-surface.md:1469 makes the post-demotion pod equivalent to a freshly warmed pod-warm pod. `DemoteSDK` removes the registry entry. EVIDENCE: pkg/adapter/sdkwarm.go:296-298 via pkg/adapter/slotsession.go:214.
FACT: The shipped gateway does not take that path: `Binder.Prepare` calls `DemoteSDK` before its first bind-sequence RPC, and `Binder.Launch`'s `ConfigureWorkspace` failure arm reclaims and returns rather than demoting and continuing. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go, `Prepare` DemoteSDK call site and the `Launch` `ConfigureWorkspace` failure branch. So the drop rule is defensive today, which is why it is one clause rather than a mechanism with its own deliverable.
DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md]: CODE-6's tier-1 `adapterclient` latch cases do not pin the new clear. What is true instead: `Client.BindEpoch` must answer zero after a successful `DemoteSDK` on a connection that had latched a non-zero epoch, and a tier-1 case asserting that belongs beside the existing latch cases. Not authored here, because this loop's grant on that file covers only repair of what its own spec edit falsified. Not tier 3 and not tier 10: no shipped call graph produces the ordering over a real transport, and CONF-1 publishes adapter obligations, of which this is not one.
UNVERIFIED: The landed §4.7.1 sentence says the coordination generation "is validated on the RPCs that carry it". The adapter in fact validates it only in `CoordinatorFence` and `CheckpointBarrier` (standing context, Settled: "`Shutdown` is unfenced"). The sentence is about §10.1's contract rather than the adapter's current enforcement, but a later round should confirm §10.1 states validation on every carrying RPC before this wording stands.
WATCHOUT: In the §15.4 block the `Shutdown` carve-in was placed AFTER the "Each bind-sequence request may carry ..." sentence and before the narrowed universal, so the paragraph reads list, reporting rule, carve-in, universal. The §4.7.1 block has the same order for the same reason. Do not "restore" the reviewer's ordering, which put the universal before the reporting rule. EVIDENCE: spec-changes.md SPEC-5 §15.4 block, second paragraph.


### [spec.2.fix-G2.1]

DECISION: SPEC-4's §6.2 pre-`running` cleanup paragraph no longer asserts a hold or an admission rule. Its closing sentences now point at §4.7.1 (which bind requests the adapter admits onto a slot identifier) and §5.2 (the reclaim hold, windowed from the deregistration to the end of the cleanup), and state that the `slot_cleanup` sub-state carries no admission rule of its own — BECAUSE `slot_cleanup` is entered at the gateway-observed failure (spec/06_warm-pod-model.md:154 for the shipped edge, spec-changes.md:596-598 for the staged one), strictly earlier than the adapter's deregistration the hold is anchored to, so the old sentence forbade in the leading interval exactly the epochless bind §4.7.1 admission rule 1 requires the adapter to admit and the create-time-reserved retry walkthrough depends on — ALTERNATIVES: re-anchoring §6.2's own hold sentence to the deregistration (the reviewer's suggested wording) was rejected because it lands a third normative copy of a window §5.2 and §15.4 already state, and an earlier round already had to reconcile two copies; deleting the closing sentence outright loses the protection that a reader of the fence alone does not read `slot_cleanup` as a label on a bindable slot; qualifying the sentence with an exception clause duplicates §4.7.1's rules in a second voice; inverting the layering so §6.2 owns the window was rejected because the hold is adapter-local and §6.2's sub-states are gateway-tracked session bookkeeping.

FACT: the reclaim hold is now stated in exactly two places in the staged text, §5.2 internally (spec-changes.md `**Slot-identifier reclaim hold.**`) and §15.4 as the published third-party contract (`**Slot-identifier reclaim hold:**`), and both window it from the deregistration of the registry entry. Every other site points at those two. A later round that adds a third statement re-opens this finding — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:556 and :666.

WATCHOUT: §6.2's per-slot sub-states are tracked per session by the gateway and are not adapter state, so any staged sentence that asks the adapter to evaluate a predicate over them is unimplementable regardless of how the window is drawn — EVIDENCE: spec/06_warm-pod-model.md:150 "Per-slot sub-states (tracked per session, not as pod-level phase; ...)".

USEFUL [the G1 report of this round]: G1's §4.7.1 rewrite had already settled the admission-rules wording and left the `#471-role-and-gateway-rpc-contract` anchor in place, which is the anchor this pointer names.


### [spec.2.fix-design-G1.1]

CORRECTS [standing context "MISTAKE: SCHEMA-1 asserts `AssignCredentialsResponse` is an empty message today, so its first field is 1" and the Open item "`AssignCredentialsResponse.bind_epoch = 1` is a hard field-number collision ... the free number is 6"]: both entries are FALSE. `schemas/lenny-adapter.proto:1033` is literally `message AssignCredentialsResponse {}` — an empty single-line message. `bind_epoch = 1` is correct and `buf` has nothing to fire on. The fields 1/2/3/reserved 4/5 that the earlier entry attributed to it belong to `AssignCredentialsRequest` (:1021-1031) and `RotateCredentialsRequest` (:1035-). The artifact that produced the error is reproducible: `awk '/^message AssignCredentialsResponse/,/^}/'` never terminates on a one-line message, so the range runs on and prints the NEXT message's body. Anyone re-checking a proto field number must use `sed -n '<line>,<line>p'` or `grep -n "message X"` first. If a later round "fixes" the number to 6 it lands a defect. EVIDENCE: schemas/lenny-adapter.proto:1021-1035.

FACT: the shipped gateway never continues a bind sequence after a `DemoteSDK` on the same connection, so the deadlock finding 3 names is a SPEC-text defect with no reachable trace today. `Binder.Prepare` calls `DemoteSDK` FIRST, before `stageWorkspace`, on a blocking-path match only; `Binder.Launch`'s `ConfigureWorkspace` failure arm calls `reclaim()` and returns, it does not demote and fall back. The spec's `ConfigureWorkspace` row nevertheless mandates the fallback, and `pkg/adapter/sdkwarm.go`'s `DemoteSDK` doc comment is written for both triggers. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:878-897 (Prepare, demote-first), :1005-1012 (Launch, reclaim-on-ConfigureWorkspace-failure); spec/04_system-components.md:673; pkg/adapter/sdkwarm.go:265-270.

FACT: `Prepare` closes its adapter connection (`cl.Close()`) and `Launch` re-dials via `b.reconnect`, so ONE bind attempt spans TWO `adapterclient.Client` instances and therefore two epoch latches. `Launch`'s first bind-sequence RPC is always epochless and always mints a fresh epoch on an entry whose session has not started. This is why the "one attempt, one epoch value" test claims (non-spec-changes.md:1245, :1369) survive: they are per-connection claims in practice. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:822, :958, :967-975.

FACT: the entry-destroying set on the adapter is wider than `Shutdown` and `DemoteSDK`. Every failure arm of `ConfigureWorkspace`, `StartSession` and `Resume` calls `releaseSessionSlot` itself. A caller-rule drop clause keyed on "an RPC the caller issues that removes the entry" therefore does NOT cover a bind-sequence RPC that failed and took the entry with it; that ordering is unreachable only because the gateway reclaims rather than retrying in place. EVIDENCE: pkg/adapter/sdkwarm.go:236,:241,:251,:298; pkg/adapter/resume.go:69-141; pkg/adapter/session.go:133,:147,:157.

DECISION: close finding 3 by folding the epoch DROP into the definition of what a caller HOLDS, in the §4.7.1 caller-rules paragraph, rather than appending a `DemoteSDK` exception to the admission rules — BECAUSE the admission rules stay untouched, §15.4 needs no edit at all (it states adapter obligations only, and the adapter's behaviour is unchanged: after a demotion it holds no entry and rule 1 admits the epochless request), and the paragraph reads as one rule instead of a rule plus a carve-out. ALTERNATIVES: (a) relax adapter rule 2 to admit a mismatched epoch when the adapter holds NO entry, re-minting — dissolves the whole exception class and needs no caller knowledge, but REJECTED because it lets a straggler request from a dead attempt re-create an entry and a workspace tree on an unowned identifier that nothing will ever reclaim, which is this proposal's own title; (b) delete "and it names no other" from the caller rule so the post-demotion sequence may simply go epochless — smaller, but leaves "when does a caller drop its epoch" unanswered, which is the unspecified-mechanism failure the loop exists to prevent; (c) leave it, on the ground that the gateway never takes the path — rejected: §4.7.1 is the normative contract and the §4.7 `ConfigureWorkspace` row mandates the path.

WATCHOUT: the reviewer's suggested replacement for finding 2 says `Shutdown` "is admitted under neither of the two rules above". In BOTH staged blocks the admission-rules paragraph sits BELOW the RPC-set paragraph being edited (spec-changes.md:632 then :634; :656 then :658). Write "below". And do NOT copy the §4.7.1 sentence word-for-word into §15.4 as the finding suggests: it cross-references "the caller rules below", and §15.4 carries no caller rules.

UNVERIFIED: whether CODE-6's `Client.DemoteSDK` clearing the latch is worth landing given no reachable trace exercises it. It is one line and the spec rule binds the client; somebody on the non-spec lane should confirm the clear is written and that non-spec-changes.md:1074's unconditional "echoes it on the attempt's later bind-sequence requests" gains the demotion clause.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:1074]: after the §4.7.1 caller-rule fix, "the client's latch holds the most recent epoch the connection reported and echoes it on the attempt's later bind-sequence requests" is an incomplete statement of the rule CODE-6 must implement. What is true instead: the latch is CLEARED when the client issues `DemoteSDK`, so a post-demotion pod-warm bind sequence is epochless. If the spec-lane fixer may not edit non-spec-changes.md, this is the correction the non-spec lane must land.


### [spec.2.fix-design-G2.1]

DECISION: Fix SPEC-4's §6.2 hold sentence by DELETING its normative claim and replacing it with a pointer at §5.2 (the hold) and §4.7.1 (the admission rules), rather than by re-windowing §6.2's own statement of the hold to the deregistration — BECAUSE §5.2 and §15.4 already carry the hold's window and §4.7.1 carries the admission rules; a re-windowed §6.2 sentence would be a THIRD normative statement of the same window, which is exactly the drift that produced this finding (an earlier round already fixed two-start-points at §5.2 and §15.4 and left §6.2 behind). — ALTERNATIVES: (a) the reviewer's suggested wording, which restates the deregistration-anchored window inside §6.2: correct but keeps a third copy alive for a future round to desynchronize; (b) delete the sentence outright: loses the reader-of-the-fence protection the rationale at spec-changes.md:617 names, and leaves `slot_cleanup` readable as a free label; (c) add "except during the leading interval" as an exception clause: hair, and it would make §6.2 assert a predicate the adapter cannot evaluate.

FACT: §6.2's per-slot sub-states are gateway-side, "tracked per session, not as pod-level phase" — EVIDENCE: spec/06_warm-pod-model.md:150. The adapter does not track `slot_cleanup` at all, so ANY adapter admission rule keyed on that sub-state is unimplementable, not merely mis-windowed. This is the structural reason the pointer form is right and the re-windowed form is only accidentally right.

FACT: `slot_cleanup` is entered at a gateway-observable failure (spec/06_warm-pod-model.md:154 `running ──→ slot_cleanup (session completes or fails)`; staged in-edge at spec-changes.md:596-598), which is strictly earlier than the adapter's deregistration that the hold starts at (spec-changes.md:556, :666). The gap between them is exactly the interval in which §4.7.1 admission rule 1 REQUIRES a bind to be admitted (spec-changes.md:634) and in which the create-time-reserved retry walkthrough (spec-changes.md:168-171) depends on that admission.

WATCHOUT: the same over-broad equivalence is restated twice more and both must move in the same edit — the Design rationale "§6.2's `slot_cleanup` sub-state is that hold on either incoming edge" (spec-changes.md:141) and the deliverable index line for SPEC-4 (summary.md:533). The implementation-checklist S4 line (implementation-checklist.md:12) says only that the paragraph "cites the §5.2 sentence S3 lands", which stays true (more true) under the pointer form — do not edit it.

FACT: non-spec-changes.md already describes the reclaim hold as anchored at the deregistration critical section, so the code lane is NOT falsified by this fix and needs no edit — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:51-60, :1532-1577.

FACT: the anchor `04_system-components.md#471-role-and-gateway-rpc-contract` is valid (heading at spec/04_system-components.md:659) and is the form already used at spec-changes.md:556 and :634; spec/06 links cross-file the same way.


### [spec.2.review-applicability.1]

FACT: The anchor sweep is still clean after the round-1 rewrite. A python script over the 31 fenced blocks below `## Staged edits` gives count 1 for the anchor blocks (indices 0,2,5,8,10,12,14,16,18,20,22,24) and 0 for every replacement/insertion, against `spec/*.md`. Every markdown fragment link inside a staged block resolves to a real heading (checked mechanically, including the four intra-file forms `#47-runtime-adapter`, `#471-role-and-gateway-rpc-contract`, `#71-normal-flow`, `#73-retry-and-resume`). No staged edit's anchor lies inside another staged edit's replaced text, so the five spec deliverables can be applied in any order. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:227-709

FACT: The three non-fenced insertion points all resolve unambiguously in the current tree. SPEC-5 §4.7.1 lands between the `*Adapter → Gateway RPCs:*` table (spec/04_system-components.md:688-692) and `#### 4.7.2` (:695); SPEC-5 §15.4 between the `**SDK-warm demotion contract:**` paragraph (spec/15_external-api-surface.md:1469) and `#### 15.4.1` (:1471); SPEC-4's fence edge after the two-line `receiving_uploads ──→ running` entry (spec/06_warm-pod-model.md:152-153), and the staged edge's column alignment and 41-space continuation indent match that entry exactly.

FACT: No shipped gate turns red on the staged spec text, re-derived after the rewrite. `grep -rn "Slot cleanup\|Scrub model\|reclaim hold\|Slot-identifier\|[Bb]ind epoch" tests/ scripts/ cmd/ pkg/` returns nothing, so the §5.2 append and the two new §15.4 blocks anchor no `lineContaining` gate. `tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65,:74-100,:138-146` reads only the §4.7 `Shutdown` row tail SPEC-1 leaves untouched. `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55,:70` drives a fixed four-edge `generalSlotEdges` slice that SPEC-4's new edge is not a member of, and SPEC-4's new prose paragraph contains neither `running ──→ failed` nor `slot_cleanup ──→ leaked`, so both loops stay green with the spec lane landed and DOCS-1 not yet.

FILED: SPEC-5's new "bind-sequence RPCs are exhaustive" sentence contradicts the epoch fence on `Shutdown`. Both §4.7.1 (spec-changes.md:632) and §15.4 (:656) close the RPC set with "Every other RPC on this contract neither carries nor reports a bind epoch ... and no epoch precondition applies to it", while §4.7.1's own caller rules (:636), the §4.7 `Shutdown` row (:276) and §15.4's own compare paragraph (:660) all have `Shutdown` carrying one as a precondition on the whole request. This sentence pair is new in this round's diff.

WATCHOUT: the sentence at spec-changes.md:12 ("Four statements are needed") is now stale — the Design carries six bolded statements (:14, :33, :75, :89, :100, :131) after the epoch and hold blocks were added. Deliberately NOT filed: the standing context records five refutations of that bookkeeping class on the materiality bar (review-log.md `### Traps`, the "bookkeeping against the deliverable index" entry). A fixer already editing the Design lead-in should correct it in passing.

USEFUL [Standing context, "the glob `*spec-changes.md` matches BOTH files"]: still true and still the first thing that bites; every read here used the full filename.

USEFUL [Standing context, "The snapshot diffs have been empty for most rounds"]: `spec-r2` and `spec-r2-start` are byte-identical to the live proposal; the real delta for this round is `diff -u spec-r1-prefix/...spec-changes.md <live>`, 360 lines.


### [spec.2.review-citations.1]

FACT: The staged-anchor sweep is still mechanically clean after this round's rewrite. A python pass over the 31 fenced blocks of `.spec-changes.md` against `spec/*.md` gives count 1 for every anchor block (indices 0,2,5,8,10,12,14,16,18,20,22,24) and count 0 for every replacement/insertion block. Do not re-run it unless `spec/` moves. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md (whole file); spec/04,05,06,07,15,29

FACT: Every file:line citation in the staged spec rationale resolves and says what is claimed: spec/04:151 (scope derived from the field set), spec/04:157 (the shipped third sentence with "per-session teardown"/"whole-pod scrub"), spec/29:586-588 and :589-591 (§29.4 Preconditions plus the interrupt addition), spec/29:669-674 (step 10's endpoint-precondition restatement), spec/29:697 ("the adapter closes the session runtime"). §29.4 step 13's append anchor "([§15.4.3](...), §28.5.3)." is at spec/29:711 and the step already carries the Basic/Standard inline exception at :709-711. — EVIDENCE: spec/29_communication-scenarios.md:586-591,:669-674,:697,:704-711

FACT: The two new SPEC-5 insertion points are exact and unambiguous. §4.7.1's `*Adapter → Gateway RPCs:*` table closes at spec/04:693 with `#### 4.7.2` at :695; §15.4's `**SDK-warm demotion contract:**` paragraph is spec/15:1469 with `#### 15.4.1` at :1471. All seven bind-sequence RPCs are rows in the §4.7.1 Gateway→Adapter table (spec/04:667-686). — EVIDENCE: spec/04_system-components.md:657-695; spec/15_external-api-surface.md:1458-1471

FACT: The "Spec sections deliberately untouched" claim that §5.2's **Non-retryable failure categories** list "is scoped to `maxConcurrentSessions > 1`" is true: the list sits inside the `**Slot retry policy (`maxConcurrentSessions > 1`)**` block at spec/05:553-560. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:553-560

FILED: SPEC-5's new bind-sequence exhaustiveness sentence contradicts the `Shutdown` epoch it is staged beside. `spec-changes.md:632` (§4.7.1) and `:656` (§15.4) both say "every other RPC on this contract neither carries nor reports a bind epoch", §4.7.1 adding "and no epoch precondition applies to it", while `:276` (§4.7 row), `:636` (caller rules), `:660` (§15.4 comparison) and SCHEMA-1's `ShutdownRequest.expected_bind_epoch = 7` (non-spec-changes.md:1028) all make `Shutdown` carry one under a precondition. Both sentences are new this round. The fix is an explicit `Shutdown` exception in both blocks, not a widening of the bind-sequence list.

WATCHOUT: §4.7.1/§15.4 say the adapter refuses a bind-sequence request "while a cleanup for the slot identifier is running", while §5.2's and §15.4's reclaim-hold paragraphs window the hold "from the critical section that deregisters the slot's registry entry", and SPEC-4's §6.2 sentence windows it as "while a slot is in `slot_cleanup`" (a state entered at "session completes or fails", spec/06:154). Three windows with the same end and three different starts. Judged below the bar this round because §6.2 explicitly defers the definition to §5.2 and every divergence is in the fail-closed direction, but a fourth restatement would make it filable. — EVIDENCE: spec-changes.md:556,:614,:634,:666; spec/06_warm-pod-model.md:150-156

FACT: Among the seven bind-sequence requests only `ResumeRequest` carries `coordination_generation` (field 14); `ShutdownRequest` carries it at 6 and `AssignCredentialsResponse` at 5. §4.7.1's "the `coordination_generation` the same requests carry" is therefore loose over the set but true of the messages that carry both. Not filed. — EVIDENCE: schemas/lenny-adapter.proto (messages PrepareWorkspaceRequest, FinalizeWorkspaceRequest, RunSetupRequest, AssignCredentialsRequest, StartSessionRequest, ConfigureWorkspaceRequest, ResumeRequest, ShutdownRequest)

USEFUL [Standing context, Traps, "The glob `*spec-changes.md` matches BOTH files"]: still true and still saves a wasted read; use the full filenames.
USEFUL [Standing context, Settled, "The staged-anchor sweep now has a mechanical, repeatable form"]: reproduced in one command, third independent confirmation.


### [spec.2.review-client-surface.2]

FACT: `Shutdown` sits in §4.7.1's own *Gateway → Adapter RPCs* table, inside the block headed
"**Contract (internal gRPC/HTTP+mTLS API — gateway ↔ adapter):**", and SPEC-5's §4.7.1 block is
inserted immediately after the *Adapter → Gateway* table that closes the same contract. So
"every other RPC on this contract" in SPEC-5 unambiguously quantifies over `Shutdown`.
EVIDENCE: spec/04_system-components.md:659-693 (heading `#### 4.7.1 Role and Gateway RPC
Contract` at :659, `| \`Shutdown\` |` row at :686, `#### 4.7.2` at :695).

FACT: there is no `ShutdownRecycle` RPC. The recycle disposition is `ShutdownRequest.recycle`
(field 5), so `Shutdown` is the single teardown RPC and the only non-bind-sequence RPC the epoch
staging touches. A reviewer who reads the standing-context phrase "a second `ShutdownRecycle`
RPC" as a distinct proto RPC will mis-scope the exhaustiveness sentence. EVIDENCE:
schemas/lenny-adapter.proto:206 (`rpc Shutdown(ShutdownRequest) returns (ShutdownResponse)`),
:1609-1636 (`RecycleScrub recycle = 5;`).

FACT: of the seven bind-sequence requests, only `ResumeRequest` carries
`coordination_generation`. `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`,
`RunSetupRequest`, `AssignCredentialsRequest`, `StartSessionRequest` and
`ConfigureWorkspaceRequest` carry none, and no bind-sequence RESPONSE carries one. The full
carrier set is SendMessage/Attach/RotateCredentials/ExtendCredentialLease/RevokeCredentials/
Interrupt/Checkpoint/SignalDeadline/Resume/CoordinatorFence/CheckpointBarrier/ExportPaths/
ReportUsage/Shutdown requests. EVIDENCE: `grep -n "^message \|coordination_generation = "
schemas/lenny-adapter.proto`; ResumeRequest at :1333 (field 14), ShutdownRequest at :1609
(field 6).

FACT: `AssignCredentialsResponse` genuinely IS an empty message, so SCHEMA-1's `bind_epoch = 1`
there is correct. CORRECTS [Standing context Settled, the "MISTAKE: SCHEMA-1 asserts
`AssignCredentialsResponse` is an empty message today" entry]: the fields that entry lists
(`session_id = 1`, `leases = 2`, `reserved 3`) belong to `AssignCredentials**Request**`, not the
response. The response is `message AssignCredentialsResponse {}` and field 1 is free; the free
number is NOT 6. EVIDENCE: schemas/lenny-adapter.proto:1021-1033.

DEFERRED [0081...non-spec-changes.md]: SCHEMA-1's field table (non-spec-changes.md:1026-1036)
still stages the epoch on seven RESPONSES plus `ShutdownRequest.expected_bind_epoch` and
`ShutdownResponse.slot_reclaim` — nine fields, no request-side field on any of the seven
bind-sequence requests. The staged spec now mandates the request side in two normative places
(spec-changes.md:632 "Each of them may carry a bind epoch on its request", :634 "A request
carrying an epoch is admitted only when the entry the adapter holds for the identifier carries
that epoch") and the code lane already assumes it (non-spec-changes.md:1075-1077 "echoes it on
the attempt's later bind-sequence requests"). What is true: SCHEMA-1 needs seven more request
fields. The standing free numbers are recorded in Settled ("PrepareWorkspaceRequest 5,
FinalizeWorkspaceRequest 6, RunSetupRequest 5, AssignCredentialsRequest 4, StartSessionRequest
12, ConfigureWorkspaceRequest 5, ResumeRequest 16"); each message reserves the number one below
for the retired `slot_id`, so do not reuse it. The spec side is right and needs no change, which
is why this loop cannot close it.

WATCHOUT: `tests/tier11_docs/credential_path_literal_sweep_test.go` sweeps only the RETIRED
pod-global credential literal directly under `/run/lenny/`. SPEC-3's widened §5.2 action list
writes the PER-SLOT literal `/run/lenny/slots/{sessionId}/` plus `credentials.json`, which the
sweep does not touch, so the new path literal in spec/05 turns no tier-11 gate red. Do not file
it. EVIDENCE: tests/tier11_docs/credential_path_literal_sweep_test.go:3-19,:57.

UNVERIFIED: `SLOT_RECLAIM_OUTCOME_UNSPECIFIED = 0` has no meaning in either staged spec block
(spec-changes.md:276 and :662 both state exactly three outcomes) and CODE-4's switch routes it
through `default: return false`, i.e. "not leaked" (non-spec-changes.md:565-566). Whether a
third-party adapter answering the zero value should be treated as conforming, non-conforming, or
leaked is unstated. Judged below the bar (proto3 zero-value convention, and
`SessionScrubOutcome` has the same shape); a later conformance lens may disagree.


### [spec.2.review-docs-alignment.1]

FACT: round 2 of the `spec` loop began with the proposal byte-identical to the r2 snapshot
outside the review log. `diff -ru -x '*review-log*' scratchpad/cp-snap/.../spec-r2
proposals/0081_...` is empty, so round 1 landed no fix and every round-1 finding was refuted.
A "read the changed sections hardest" reading order has nothing to bite on in this round.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r2

FACT: `Shutdown` is a Gateway → Adapter RPC row in the SAME §4.7.1 table the seven
bind-sequence RPCs sit in (spec/04_system-components.md:665-686, the `Shutdown` row at :686,
the table opening at :665). So "every other RPC on this contract" in SPEC-5's staged §4.7.1
paragraph and in its §15.4 twin unambiguously reaches `Shutdown`. That is what makes the
carries/precondition contradiction real rather than a scoping quibble.
EVIDENCE: spec/04_system-components.md:659,:665-686

WATCHOUT: the orchestrator's already-fixed list names "the corrected mechanism requires
bind-sequence requests to carry the epoch, and SCHEMA-1 stages no request field for it" and
"CODE-6 stages no request echo and no epoch source for the seven handlers". Reading the
current text, both look UNFIXED: SCHEMA-1's field table stages exactly nine fields, of which
the only request field is `ShutdownRequest.expected_bind_epoch` (non-spec-changes.md:1026-1036,
"one enum and nine fields" at :992), while staged §4.7.1 still says "Each of them may carry a
bind epoch on its request" (spec-changes.md:632) and staged §15.4 still makes "mints a fresh
epoch for a request echoing the current one" a non-conformance (:658). The code lane's only
carrier is a per-connection latch that is read for `ShutdownReclaim` alone
(non-spec-changes.md:955-966). I did NOT file it, because re-litigating a named already-fixed
item is barred and because its remedy lands in the schema/code lane this loop may not edit.
Someone with authority over the non-spec loop should confirm the request-side echo really was
retired or really is staged; if it was retired, §4.7.1:632 and §15.4:656-658 are the sites
that still assert it.
EVIDENCE: spec-changes.md:632,:656-658; non-spec-changes.md:992,:1026-1036,:955-966

DEFERRED [docs/reference/adapter-contract.md]: the §15.4 bind-epoch and reclaim-hold blocks
publish four new third-party adapter obligations (mint-per-attempt, admission rules, the
epoch comparison on `Shutdown`, the three reclaim outcomes) plus the reclaim hold. The
reader-facing mirror of that contract carries a `Shutdown` row and a status-code table and
gains none of it. Out of scope this loop (docs remedy), and the standing context already
holds a larger DEFERRED on the same file's line 75.

DECISION: filed exactly one finding, the `Shutdown`-versus-"every other RPC" contradiction —
BECAUSE this loop only accepts findings whose remedy lands in the staged spec edits, and the
docs-alignment lens's ordinary output (a missing or wrong `docs/` edit) is structurally out of
scope here — ALTERNATIVES: I built and dropped three. (1) The connect-stage `slot_assigned`
widening: its observable outcome (a leaked slot in `slot_assigned` on the create-time-reserved
path) lands only in the summary and in SPEC-4's commentary, but stating it in §6.2 amounts to
asking for the edge SPEC-4 deliberately withholds, so it reads as re-litigating an accepted
scope call. (2) `WARM_POOL_EXHAUSTED` gaining a new cause: standing context :457 already
records this as a nearly-filed MISTAKE on a pre-existing enumeration gap, and the remedy is a
docs edit. (3) Forward-RPC fencing missing from the "Edge cases and accepted failure modes"
section: it has a thorough home in the summary's "Defects in the shipped tree that this
proposal does not stage", which is a section placement judgement rather than a defect.


### [spec.2.review-edit-sites.2]

FACT: The snapshot `spec-r2` is byte-identical to the live proposal except the review log; the
real delta for this round is `spec-r1-prefix` → live (the per-attempt epoch rewrite, the
"did not complete" predicate sweep, the occupancy→reclaim-hold rename, the §4.7 whole-request
fence, the §29.4 bullet deletion from `## Spec sections deliberately untouched`, and the
"deliberately untouched" §5.2 non-retryable bullet's new justification). Run
`diff -rq scratchpad/cp-snap/.../spec-rN proposals/...` over every snapshot first.
EVIDENCE: scratchpad/cp-snap/0081_.../spec-r1-prefix vs proposals/0081_...

FACT: The amendment's new identifiers do not collide anywhere.
`grep -rn "bind epoch\|bind-epoch\|bind_epoch\|reclaim hold\|superseded" spec/ docs/ schemas/ charts/`
returns only unrelated `superseded` uses (checkpoint manifest reasons at spec/10:148,:153,
spec/16:198-199; admin token rotation at spec/15:865; ADR front-matter). "epoch" in spec/ is
only §25's `ops_lock_epoch`. No new-term sweep is owed.
EVIDENCE: spec/16_observability.md:198; spec/10_gateway-internals.md:148

FACT: The bind-sequence RPCs are carried on `LNK-POD-GRPC` but are on NO §28 channel register
row and NO §28.5.1 contract card. The gateway-to-pod channels there are CH-ATTACH,
CH-CHECKPOINT, CH-FENCE, CH-BARRIER, CH-PODHEALTH (plus pod-to-gateway CH-ADAPTEREVENTS), so
SCHEMA-1's seven request/seven response fields add no §28 row. Re-derived mechanically this
round; the "deliberately untouched" §28 bullet holds.
EVIDENCE: spec/28_communication-channels.md:106,:118-123

FACT: `DemoteSDK` DOES clear the adapter's registry entry (`anyRegisteredSession` →
`noteRuntimeClosed` + `releaseSessionSlot`), so the §4.7 `ConfigureWorkspace` failure path's
pod-warm fallback meets no surviving started entry and is NOT broken by the new epochless
admission rule. Two rounds could plausibly build that finding; it dies here.
EVIDENCE: pkg/adapter/sdkwarm.go:295-300; spec/15_external-api-surface.md:1470

FILED: §4.7's `ConfigureWorkspace` row (spec/04:673) publishes "Idempotent (same `cwd` path is
safe to send twice)" and the shipped adapter implements it with the ONLY
`idempotentRepeat=true` call in the tree (sdkwarm.go:217, whose comment cites "the §4.7
table"). Staged §15.4 (spec-changes.md:658) makes admitting an epochless bind-sequence request
onto a started identifier non-conformance, and `ConfigureWorkspace` is a bind-sequence RPC
(:632). The row is in no edit list and the non-spec lane deliberately keeps the repeat arm
alive (non-spec-changes.md:954).
EVIDENCE: spec/04_system-components.md:673; pkg/adapter/slotsession.go:79-86

DECISION: I did NOT file "the reclaim hold's bound is stated only under a
`maxConcurrentSessions > 1` heading" (standing Open #597). BECAUSE SPEC-3's appended pointer
clause explicitly generalises the bullet — "That per-slot cleanup is the one the **Slot
cleanup:** bullet below states, on a pod of either concurrency" — and the bullet's formula
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` degenerates to the whole
`cleanupTimeoutSeconds` at concurrency 1, which is exactly what the edge-case bullet at
spec-changes.md:184 says. The Open's premise ("the formula is stated only under a
`maxConcurrentSessions > 1` heading") is true of the heading and false of the applied text.
ALTERNATIVES: filing it as an unlisted §5.2 edit site; rejected on the pointer clause.
EVIDENCE: proposals/0081_....spec-changes.md:552-556; spec/05_runtime-registry-and-pool-model.md:545

USEFUL [Settled, "The new-identifier sweep is complete and empty"]: saved a full sweep; I only
had to re-run it for the four amendment terms, which came back clean too.

UNVERIFIED: whether §15.4's "An adapter that reports zero on a bind-sequence response does not
conform" is satisfiable by `ResumeResponse` and `ConfigureWorkspaceResponse` on a pod where the
entry was created by an EARLIER RPC of a different attempt. Nobody has walked the seven
responses against the mint/echo rules one by one; a mechanism lens should.


### [spec.2.review-feasibility.1]

DECISION: filed exactly one finding, the SPEC-4 §6.2 hold sentence keyed on the DURATION of the
`slot_cleanup` sub-state rather than on the hold's own window — BECAUSE the sub-state is entered at
the gateway-observable event ("session completes or fails" at spec/06:154; "the bind is abandoned or
fails" in SPEC-4's new edge at spec-changes.md:596) while §5.2/§15.4 window the hold from the
adapter's DEREGISTRATION (:556, :666), so the leading interval of `slot_cleanup` is a window in which
§6.2 forbids a bind that §4.7.1 rule 1 (:634) and the proposal's own create-time-reserved edge case
(:168-171) both require the adapter to ADMIT — ALTERNATIVES: I considered and dropped the §7.1
"spends one of the attempts the retry policy allows" clause (:24-26) as self-refuting once a second
client request's own attempt 1 is counted; and the exclusive-pool bound for the hold (§5.2's cleanup
timeout lives under a `maxConcurrentSessions > 1` heading), which is already the standing Open on
"Nothing in the staged spec bounds the reclaim hold on an exclusive pool" and is not this lens's.

FACT: the §6.2 per-slot fence's two `slot_cleanup` in-edges are triggered by gateway-observable
events, never by the adapter's deregistration. `running ──→ slot_cleanup (session completes or
fails)` — EVIDENCE: spec/06_warm-pod-model.md:154. Any staged sentence that says "while a slot is in
`slot_cleanup`, the adapter <does X>" therefore claims X for an interval that starts before any RPC
reaches the adapter. Check every future §6.2 prose sentence against this.

FACT: the hold now has THREE candidate start points in the document and only two of them agree.
§5.2 (:556) and §15.4 (:666) both say deregistration; §6.2 (:614) says the whole `slot_cleanup`
state. The earlier round's filed-and-fixed "TWO different start points" finding was about
"creation of its registry entry" vs "cleanup still running" and did NOT sweep §6.2. — EVIDENCE:
spec-changes.md:556,:614,:666.

WATCHOUT: the §6.2 sentence LOOKS like a harmless restatement ("The hold sentence adds no edge",
:617) and the standing-context DECISION entry records its wording approvingly, so a reader who
checks only that the sentence mints no edge passes over it. The defect is the quantifier, not the
edge. — EVIDENCE: spec-changes.md:614,:617.

FACT: the exclusive bind path spans TWO connections per §7.1 attempt kind, and that is FINE for the
epoch. `Binder.Prepare` runs PrepareWorkspace→AssignCredentials then `cl.Close()`
(pkg/gateway/podlifecycle/podsession/binder.go:957), and `Binder.Launch` `b.reconnect`s before
`StartSession`/`ConfigureWorkspace` (:975, :1008-1013). Those are two different §7.1 attempts (the
creation finalize block and the §15.1 start), so Launch legitimately sends the epochless form and is
minted a fresh epoch under rule 1. A lens that reads them as one attempt will file a false
caller-echo finding.

FACT: no §28 register row names any of the seven bind-sequence RPCs, so SCHEMA-1 widening seven more
messages adds no §28 row and the "deliberately untouched" §28 bullet holds even though it argues only
from `Shutdown`. — EVIDENCE: `grep -n "PrepareWorkspace\|FinalizeWorkspace\|RunSetup\|AssignCredentials\|StartSession\|ConfigureWorkspace" spec/28_communication-channels.md` returns only prose at :1124, :1404, :1411, :1819.

FACT: the updated "deliberately untouched" bullet on §5.2's non-retryable list is accurate. The list
sits under `**Slot retry policy (`maxConcurrentSessions > 1`).**` — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:571 (heading) and the `**Non-retryable failure
categories:**` bullet below it.

UNVERIFIED: `examples/runtimes/echo/` (spec/15_external-api-surface.md:1467, the paragraph SPEC-5
inserts immediately after) does not exist in the tree; the reference runtimes are under
`cmd/runtimes/`, which is also what spec/18_build-sequence.md:121 says. Pre-existing spec-vs-tree
drift in the sentence SPEC-5 sits beside, not this proposal's edit site. Somebody outside 0081 should
own it.

USEFUL [standing context, Settled "The bind sequence is a closed set of SEVEN RPCs" and Traps "Do NOT
read `pkg/sandbox/slotstate` as adapter-side"]: those two together are what made the §6.2 window
mismatch visible; without them the §6.2 sentence reads as an adapter statement in an adapter-owned
section.


### [spec.2.review-fresh.1]

FACT: `spec-changes.md` is byte-identical to the round-2 snapshot (`diff -ru` over the snapshot dir reports only the review log as differing), so the "read the diff first" instruction had nothing to point at this round. The whole staged spec text is equally old. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r2/

FACT: `Shutdown` DOES carry an epoch under this proposal, and both SPEC-5 blocks contain a sentence that denies it ("Every other RPC on this contract neither carries nor reports a bind epoch ... and no epoch precondition applies to it"; §15.4's version prefixes "That list is exhaustive"). Filed. The sentence was written to close the earlier finding "§4.7.1's epoch-reporting rule reaches every entry-resolving RPC while §15.4 and the staged proto cover only seven" — the fix over-reached past `Shutdown`. EVIDENCE: spec-changes.md:632, :656 against :242, :636, :660.

FACT: `spec/04_system-components.md:673` mandates a bind path the epoch admission rule refuses: on `ConfigureWorkspace` failure the gateway "calls `DemoteSDK` with a 5s timeout and falls back to pod-warm materialization", inside one bind attempt that has already observed epochs on `PrepareWorkspace`/`FinalizeWorkspace`/`RunSetup`/`AssignCredentials`. `DemoteSDK` (and `ConfigureWorkspace`'s own failure arm) removes the registry entry, so the fallback's echoed epoch matches nothing and §4.7.1/§15.4 require a refusal. Filed. EVIDENCE: spec/04:673; pkg/adapter/sdkwarm.go:250-253,:296-298; spec-changes.md:634,:636,:658.

FACT: the gateway does not implement that fallback today — `binder.go:1009-1012` reclaims on a `ConfigureWorkspace` error and never calls `DemoteSDK`, and `RefusalReason` is consumed nowhere outside the generated pb. The break is therefore spec-level, not code-level, which is why it belongs to this loop. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1008-1012; `grep -rn RefusalReason pkg/ cmd/ --include=*.go` returns nothing outside `.pb.go`.

WATCHOUT: do NOT file "the epoch and `coordination_generation` travel on the same messages" (spec-changes.md:630) as false. Only `ResumeRequest` (:1405) and `ShutdownRequest` (:1635) among the epoch-bearing messages declare `coordination_generation` in `schemas/lenny-adapter.proto`, BUT spec/28:237, :393, :1675 and spec/29:450, :622 all assert the pod validates the stamp "on every gateway-to-pod RPC". The proposal restates an existing spec claim; the spec-vs-proto gap is pre-existing. I burned a verification pass on this.

WATCHOUT: the "per-chunk epoch mint" line of attack on `PrepareWorkspace` (a client-streaming RPC whose caller cannot echo until the stream ends, so every message is epochless and §4.7.1 makes the adapter mint a fresh epoch per message) is a dead end. Ownership still lands on the last mint and the caller echoes the reported value on `FinalizeWorkspace`, so the fence survives; only the counter is noisy.

WATCHOUT: the SPEC-3 sentence "A cleanup on that path that does not complete is still accounted: the adapter's `Shutdown` response for that reclaim does not report a clean exit" over-claims — the adapter's own `releaseSessionSlot` compensations (a failed `ConfigureWorkspace`, a failed manifest write, CODE-2's start rollback) are cleanups on that path with no `Shutdown` answering them, and `releaseSessionSlot` discards `removeSlotTree`'s error (pkg/adapter/slotsession.go:216-220). I did NOT file it: the only available fix is a bind-path qualifier, which the standing trap "Do NOT add a bind-path or a start-state qualifier to §5.2's leak-accounting sentence" bars.

FACT (verified, so nobody re-verifies): every "reads, verbatim" anchor in the staged edits still resolves and is unique enough to apply — spec/04:157, :686, :853(step 5), spec/05:453, :545, :555, spec/06:151-152(fence), :234, spec/07:23, :210, :213, :214, :402-410, spec/29:711. Every markdown anchor used in the new text resolves to a real heading (`#47-runtime-adapter`, `#471-role-and-gateway-rpc-contract`, `#479-...`, `#1542-rpc-lifecycle-state-machine`, `#101-horizontal-scaling`, `#62-pod-state-machine`, `#52-pool-configuration-and-execution-modes`, `#71-normal-flow`, `#73-retry-and-resume`, `#49-credential-leasing-service`).

FACT: the §28 "deliberately untouched" claim holds. §28.5.1 is per-channel (CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER, CH-PODHEALTH), states outright that "The specification does not state the individual message names of the stream in prose", and no register row names `Shutdown` or any bind-sequence RPC. EVIDENCE: spec/28_communication-channels.md:205-240.

FACT: the seven bind-sequence RPCs are exactly the entry-creating set in the tree. `ensureSlotStateLocked` has five call paths — staging.go:134,:181,:337 (PrepareWorkspace/FinalizeWorkspace/RunSetup), slotcreds.go:26 (AssignCredentials) and slotsession.go:75 (`claimSessionSlotUnderLock`, reached from StartSession, Resume and SDK-warm ConfigureWorkspace). The list is complete; do not re-derive it.


### [spec.2.review-kubernetes.1]

DECISION: filed exactly one finding, on the fix-stage exhaustiveness sentence SPEC-5 added this round — BECAUSE it is a self-contradiction inside one staged block that would make a conforming third-party adapter skip the whole epoch fence — ALTERNATIVES: filing it as two findings (one per block, §4.7.1 and §15.4) was rejected to save a verifier pair, but the fixer MUST touch both sites; the recorded pattern on this proposal is that a fixer treating two sibling sentences as one leaves one standing.

FACT: the Kubernetes-idiom lens is structurally inert on the bind-epoch/reclaim-hold amendment. The epoch is an in-memory `int64` on an adapter registry entry and the hold an in-memory map key: no CRD, no status subresource, no finalizer, no informer, no admission webhook, no etcd write. Standing-context Settled #330 states this and I re-derived it against the staged text: SPEC-1..SPEC-5 add no RBAC, NetworkPolicy, ServiceAccount, chart or podspec surface. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:596 ("pod-local, and never persisted"), :654 ("held in memory and never persisted")

FACT: SPEC-5 §4.7.1's citation of §10.1 for the coordination generation resolves and is accurate. spec/10_gateway-internals.md:30 reads "Each session row carries a `coordination_generation` counter. When a replica takes over coordination ... it increments the generation", which supports the staged "the generation names the gateway replica that speaks for the session ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling))". Do not re-verify. — EVIDENCE: spec/10_gateway-internals.md:30; spec-changes.md:630

WATCHOUT: `Shutdown` IS a Gateway → Adapter RPC row in §4.7's table, so any sentence quantifying over "every other RPC on this contract" reaches it. A future edit that re-scopes the bind-sequence list must say what it means for `Shutdown`, which carries `expected_bind_epoch` as a precondition and is not a bind-sequence RPC. — EVIDENCE: spec/04_system-components.md:686; proposals/0081_.../0081_....spec-changes.md:632,:656,:276

MISTAKE: the round-1 fix that closed "§4.7.1's epoch-reporting rule reaches every entry-resolving RPC while §15.4 covers only seven" overshot. It replaced the wide predicate with a closed seven-item list plus an exhaustiveness clause, and the clause now excludes `Shutdown` from carrying an epoch at all, three paragraphs above the caller rule that requires the caller to echo the epoch "on a `Shutdown` for the session". The pre-fix text (spec-r1-start snapshot) had no such clause. Fix-stage text is where this round's only finding lived.

UNVERIFIED: whether §10.1:30's "Pods validate the generation on every gateway→pod RPC" reaches the compensating `Shutdown`. Standing Settled #62 and #335 say only `CheckpointBarrier` and `CoordinatorFence` enforce it in code, so the spec statement is already loose. This proposal's §7.1 reclaim now depends on `Shutdown` reaching the adapter from a replica whose generation may be stale (§7.2 step 4 bumps it, but only after step 3). Pre-existing spec-vs-code divergence, nothing staged; a reliability or code-lane reviewer should decide whether §7.1 owes a carve-out.

USEFUL [Standing context #514]: it records the Kubernetes `DeleteOptions` analogy in SPEC-1's §4.1 replacement as already adjudicated below the bar (a real `DeleteOptions` precondition mismatch is a 409 Conflict, while an epoch mismatch here is a SUCCESSFUL `superseded` response). That is squarely this lens's surface and I would have spent a finding on it. The clause claims only the classification (precondition, not scope selector), so it stands.

USEFUL [Standing context #513]: the `*spec-changes.md` glob matches both the spec and the non-spec staging file. Used the full filenames throughout.


### [spec.2.review-mechanism.1]

CORRECTS [Standing context / Open, "`AssignCredentialsResponse.bind_epoch = 1` is a hard field-number collision"] and [Traps, "MISTAKE: SCHEMA-1 asserts `AssignCredentialsResponse` is an empty message today"]: both are WRONG and the proposal is right. `schemas/lenny-adapter.proto` declares `message AssignCredentialsResponse {}` — a genuinely empty message, so field 1 is free and SCHEMA-1's `bind_epoch = 1` is correct. The fields the log attributes to it (`session_id = 1`, `leases = 2`, `rotation_trigger = 3`, `reserved 4`, `coordination_generation = 5`) belong to `RotateCredentialsRequest`, which is the message immediately BELOW `AssignCredentialsResponse` in the file, so an `awk`/`sed` window that starts one message early reads the wrong body. Nobody should "fix" SCHEMA-1 to field 6. EVIDENCE: schemas/lenny-adapter.proto, `awk '/^message AssignCredentialsResponse/,/^}/'` returns one line.

FACT: `coordination_generation` exists on exactly 14 request messages and on NO response message. Of the eight messages SPEC-5 makes epoch-carrying, only `ResumeRequest` (:1405) and `ShutdownRequest` (:1635) also carry it; `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`, `RunSetupRequest`, `AssignCredentialsRequest`, `StartSessionRequest` and `ConfigureWorkspaceRequest` carry none. The generation instead rides the forward RPCs the epoch deliberately does NOT fence (`Attach`, `Interrupt`, `RotateCredentials`, `ExtendCredentialLease`, `ExportPaths`, `ReportUsage`, `Checkpoint`), so the two field sets are nearly disjoint. Command: `awk '/^message /{m=$2} /coordination_generation = /{print NR": "m}' schemas/lenny-adapter.proto`. EVIDENCE: schemas/lenny-adapter.proto:981,1009,1058,1082,1103,1126,1186,1317,1405,1460,1492,1548,1593,1635

FACT: the SDK-warm demotion path is NOT a hole in the epoch admission rules, and it looks like one. `DemoteSDK` does `releaseSessionSlot(sessionID)` (pkg/adapter/sdkwarm.go:296-301), which removes the registry entry, and §4.7.1 rule 2 refuses an epoch-carrying request when the adapter holds no entry at that epoch. It is safe only because `Binder.Prepare` sends `DemoteSDK` BEFORE `stageWorkspace`, so no bind-sequence RPC has minted an epoch on that connection yet. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:878-897 (demote) then :915-951 (stage/finalize/setup/creds).

FACT: `Binder.Prepare` and `Binder.Launch` run on two different connections (`Prepare` ends with `cl.Close()` at binder.go:957; `Launch` calls `b.reconnect` at :982), so Launch's `StartSession`/`ConfigureWorkspace` is epochless against an existing unstarted entry and re-mints. That is legal under §4.7.1 rule 1 only because §7.1 counts the creation finalize block and the §15.1 start transition as two separate bind attempts. Do not "fix" the caller rule to make Launch echo Prepare's epoch.

WATCHOUT: the newest fix-stage text is the two `The bind-sequence RPCs are ...` paragraphs (spec-changes.md:632 and :656). Both close with a universal about "every other RPC on this contract", and `Shutdown` is an other RPC that carries the epoch, so the universal is false in both blocks. Filed. EVIDENCE: spec-changes.md:632,:636,:656,:660,:276

WATCHOUT: `slot_cleanup` is entered at the FAILURE EVENT, not at the adapter's deregistration. spec/06:154's trigger is "(session completes or fails)" and SPEC-4's new edge's trigger is "(the bind is abandoned or fails ...)". So SPEC-4's prose sentence "While a slot is in `slot_cleanup`, on either incoming edge, the adapter holds the slot's identifier and admits no bind onto it" (spec-changes.md:614) starts the hold a whole gateway round trip earlier than §5.2's "from the critical section that deregisters the slot's registry entry" (:556, :666). This is the same two-start-points defect an earlier round filed and fixed at the §5.2/§15.4 sites, reintroduced at the §6.2 site. Filed. EVIDENCE: spec/06:154; spec-changes.md:596-598,:614,:556,:666,:168-171

FACT (the reason the epoch fence actually works, re-derived): supersession is only reachable against an UNSTARTED entry, because rule 1 refuses an epochless bind onto an identifier whose session has started. So the §4.7 row's "a superseded attempt ... can hold no live state there" is sound rather than optimistic, and the "late reclaim tears down a running session" ordering the amendment exists for is genuinely closed. Do not re-derive it as open.

OPEN: does the hold survive its own timeout? §5.2's `**Slot cleanup:**` bullet says a cleanup that fails leaves the slot `leaked` and "not reclaimed until pod termination", so the hold's terminal ("until the cleanup that reclaims it has finished") never occurs, yet both staged blocks say the timeout "bounds the hold". Filed as a mechanism finding; a human may prefer to answer it by saying the hold is deliberately permanent on a leaked slot and deleting the two bound sentences.


### [spec.2.review-operational.1]

DECISION: returned an EMPTY findings list for the operational-consistency lens on the spec staging — BECAUSE the whole delta this round (the bind epoch, the reclaim-hold rename and re-windowing, the "did not complete" leak predicate) is in-memory adapter state and prose; it mints no metric, no alert, no CRD condition and no operator-visible counter, and every observability sentence it touches was already in `spec/` before the edit — ALTERNATIVES: I built and dropped five candidates, listed below, each on evidence.

USEFUL [Standing context #226]: "No alert anywhere references any slot metric" saved the whole alert half of this lens. Independently re-confirmed this round: `grep -n "lenny_slot\|lenny_adapter_leaked\|pod_retirement\|session_reuse" pkg/alerting/rules/*.go` returns nothing, and `spec/16_observability.md`'s alert catalog rows carry no slot metric. EVIDENCE: pkg/alerting/rules/*.go (zero hits); spec/16_observability.md:456 is the nearest adapter-metric alert and is the credential-rotation ceiling.

USEFUL [Standing context #107, #227, #228]: the pre-existing `lenny_adapter_leaked_slots` / `lenny_slot_pod_replacement_total` divergences. Without them I would have filed SPEC-3's naming of the gauge as an inventory gap.

FACT: `lenny_adapter_leaked_slots` is named in `spec/` at exactly two shipped sites and in NEITHER metric inventory. SPEC-3's §5.2 append makes it a third *spec/05* mention rather than a new claim. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545 ("surfaced via the `lenny_adapter_leaked_slots` gauge"); spec/06_warm-pod-model.md:160 ("`lenny_adapter_leaked_slots` gauge, labeled by `pod_id` and `pool`"); `grep -n leaked spec/16_observability.md` returns zero.

FACT: the seven bind-sequence RPCs SCHEMA-1 now widens appear on NO §28 contract card and in no §28 register row, so the "deliberately untouched · §28's registers" entry holds for the request-side fields as well as for `ShutdownRequest`. EVIDENCE: `grep -n "StartSession\|PrepareWorkspace\|AssignCredentials\|FinalizeWorkspace\|RunSetup\|ConfigureWorkspace" spec/28_communication-channels.md` returns one hit, spec/28:1124, and that is the CH-RUNTIMEOPS degradation row naming `AssignCredentials` as a fallback step rather than a card subject.

FACT: `superseded` is already bound in `spec/` as a `manifest_reason` value on the partial-checkpoint manifest, including as a closed metric label domain. It is a different surface from the new `Shutdown` reclaim outcome and creates no inventory conflict, but a lens grepping the word will land there first. EVIDENCE: spec/16_observability.md:198 (`manifest_reason`: `timeout` | `stream_truncated` | `superseded` | `quota_exceeded`); spec/10_gateway-internals.md:157.

WATCHOUT: the five operational candidates I built and dropped, so nobody rebuilds them.
(1) "SPEC-3 asserts a gauge spec/16's inventory does not carry" — pre-existing at two shipped sites, above.
(2) "the withheld `ReportSessionScrub` falsifies spec/16:128's `lenny_pod_session_reuse_count` row" — the row reads "number of sessions served by a single pod", so withholding a report for a session the pod never ran keeps it true. EVIDENCE: spec/16_observability.md:128.
(3) "the incomplete pre-`running` leak never reaches the adapter's `/healthz` `leaked_slots` count that spec/06:160 promises" — true and pre-existing; the gauge is gateway-emitted despite its name, which Standing context #355 already records as a dead end.
(4) "the §5.2 reclaim-hold paragraph is concurrency-independent while the per-slot cleanup timeout that bounds it lives in a `maxConcurrentSessions > 1`-scoped bullet" — the staged append's own pointer clause ("That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency") carries the bullet across the boundary, and Standing context #357 already marks the whole scoping family a dead end.
(5) "generalising the **Slot cleanup:** bullet 'on a pod of either concurrency' generalises the CRD admission rule inside it, which the code applies only at `maxConcurrentSessions > 1`" — the same paragraph disclaims it in the next breath ("Nothing else in the bullet changes: its trigger, the formula, the CRD validation rule ... stand as written"), and the pointer sentence is about the cleanup operation rather than the webhook. Below the bar; Standing context #121 has the code fact if a later round wants it.

OPEN: SPEC-3's §5.2 clause gives the accounting ground for only one of §7.1's two "did not complete" arms — it reads "the adapter's `Shutdown` response for that reclaim does not report a clean exit", which has no referent for the arm where the adapter never answers at all. §7.1 states both arms (spec-changes.md:49-50, :344). I judged this under-specified rather than false and did not file it; a precision-lens round may disagree. EVIDENCE: proposals/0081.../0081....spec-changes.md:554 versus :49-50.


### [spec.2.review-performance.2]

DECISION: returned an EMPTY findings list for the performance / scalability / failure-mode lens on
the spec staging — BECAUSE the capacity half is structurally inert on this delta (re-derived
independently, see FACT below) and every reliability regression I could construct against the
shipped design lands in one of three places that bar a file: the proposal's own "Edge cases and
accepted failure modes" list, the standing Traps dead-end list, or a defect the standing context
already records as pre-existing and explicitly not 0081's. ALTERNATIVES built and dropped, each
named below so the next performance lens does not rebuild them.

FACT: `diff -rq` of the round-2 snapshot against the live proposal shows ONLY the review log
differs — `.spec-changes.md` is byte-identical to what spec.2 round 1 read. So on this staging
there is no "newest, least-examined text" to read first; the reading-order instruction has no
purchase and the whole file is equally aged. EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../spec-r2 proposals/0081_...` returns one line, the review log.

FACT (re-derived, confirms `[spec.1.review-performance.1]` and `[spec.5.review-performance.2]`):
the staged spec edits create NO control-plane or data-plane write. No etcd status write, no
Postgres row, no Redis key, no new informer/watch, no new metric series, no new store-backed
value. The only new per-unit-of-work cost is one extra `Shutdown` RPC per FAILED bind on a
connection the failed attempt already holds, plus an `int64` on seven request/response messages.
At Tier 3 (§5.2's own worked example is "500 pods", spec/05:552) the multiplier is the bind-FAILURE
rate, not the request rate, so there is no amplification term to compare against a budget.
EVIDENCE: spec-changes.md:630-636 (§4.7.1 block), :654-666 (§15.4 blocks), :344 (§7.1 paragraph).

FACT: the §5.2 whole-pod replacement threshold is `ceil(maxConcurrentSessions / 2)`, so the
proposal's "at `maxConcurrentSessions: 2` a single windowed failure already reaches the threshold"
(spec-changes.md:154) is arithmetically correct. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561.

FACT: `SlotID: req.SessionID` is set in the claimer, so §7.1's staged "The slot identifier is the
session identifier" is true against the tree and is not a proposal-local coinage.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:760.

MISTAKE nearly filed, four reliability dresses. Recording all four because each reads as a live
top-tier regression on a cold read and each cost me real time.
 (1) "The reclaim hold refuses a §5.2-placed retry that lands during the ADAPTER's own slot-failure
     cleanup (not a §7.1 reclaim), spending the default single retry and turning a retryable slot
     failure into a client-visible exhaustion." Dropped: spec-changes.md:178-191 accepts exactly
     this, and its wording is NOT scoped to the §7.1 reclaim — "a retry placed onto a pod whose
     cleanup for that identifier is still running ... consumes one of the attempts the §5.2 retry
     budget allows". The ordinary slot-failure cleanup is inside that bullet.
 (2) "§5.2:556's `**Fresh workspace guarantee**` clause (`even if the failed slot's cleanup has not
     yet completed`) is falsified by the hold, and spec-changes.md:489 asserts the bullet stands as
     written." Dropped twice over: it is the standing dead end at review-log:419 (three lenses built
     and dropped it), AND the clause's own claim is about non-INHERITANCE, which a refused bind
     satisfies vacuously. Do not rebuild it on the "the hold refuses it" framing either; that is
     dress (1).
 (3) "The new pre-`running` `leaked` class holds Redis slot-counter occupancy for a session that
     `rollback-without-persist` never wrote to Postgres, so the post-Redis-reset rehydration
     (`GetActiveSlotsByPod`, state='active') cannot recover it and the pod is over-assigned and
     never retires." Dropped: the same loss applies to every SHIPPED leaked slot, whose session row
     is not `active` either, so it is a property of §6.2:160's Redis-equivalence claim rather than
     of this delta — and the standing context already records that claim as "already wrong against
     the tree before this proposal ... not 0081's". EVIDENCE: spec/06_warm-pod-model.md:160;
     spec/05_runtime-registry-and-pool-model.md:551 (rehydration reads Postgres); review-log:551.
 (4) "A gateway replica lost mid-bind sends no reclaim (the no-re-dial rule bars a successor from
     one), so a started entry refuses its identifier for the life of the pod, where the shipped
     idempotent `ensureSlotStateLocked` admits." Dropped: spec-changes.md:185-187 states the
     terminal ("a reclaim the adapter never answers leaves the identifier refusing every further
     attempt until the pod terminates. This is accepted rather than closed"), and the shipped path
     is not actually better, because `claimSessionSlotUnderLock` refuses a second start anyway.

WATCHOUT: the reclaim hold's stated bound has NO enforcement point. §15.4 publishes "The per-slot
cleanup timeout ([Section 5.2]) bounds the hold" (spec-changes.md:666) and SPEC-3 repeats it
(:556), but the Settled entry "The adapter enforces NO per-slot cleanup timeout of its own" still
holds, and the staged code releases the hold by `defer` whatever the outcome (review-log:3932). So
the published bound is descriptive, not enforced. `[spec.1.review-performance.1]` noted this and
did not file it; I agree it is not filable, because the unbounded terminal is the accepted failure
mode at spec-changes.md:186-187. Do not file it as a fifth dress.

USEFUL [review-log Traps: "Dead end: `SlotID == SessionID` falsifies §5.2's `new slot` and the Fresh
workspace guarantee" (:419), "MISTAKE nearly filed, five performance dresses" (:425), "three
capacity dresses in the round-2 performance lens" (:464), and the §6.2:160 UNVERIFIED (:551)]:
between them these four entries killed four of my five candidates. The Traps block is doing its
job on this lens specifically; a performance reviewer should read :419, :425, :464 and :551 BEFORE
opening the spec, not after.

OPEN: the exclusive-pool bound question `[spec.1.review-performance.1]` left open (review-log:599)
reads to me as CLOSED by the fix that landed: SPEC-3's appended pointer clause "That per-slot
cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency"
(spec-changes.md:554) carries the whole bullet, its `max(cleanupTimeoutSeconds /
maxConcurrentSessions, 5)` formula included, across the concurrency boundary. Somebody with
authority over the standing context should retire that OPEN rather than let a later lens re-file
it.


### [spec.2.review-reliability.1]

DECISION: returned ZERO findings on the staged spec edits — BECAUSE every recovery/redelivery path I traced through the r1→r2 fix round's rewrite (the per-attempt epoch, the request-echo rule, the reclaim hold, the broadened "did not complete" leak predicate) closes correctly, and the three candidates I built each died on evidence recorded below — ALTERNATIVES: I priced and dropped (a) a compare-then-act atomicity gap in §15.4's `Shutdown` epoch comparison, (b) a hold-end-point contradiction between the staged §6.2 prose and §5.2, (c) a `ConfigureWorkspace` idempotency contradiction. Each is written up below so nobody rebuilds them.

FACT: the r1→r2 fix round is the ONLY real spec-changes delta in this window. `spec-r2` and `spec-r2-start` are byte-identical to the live proposal except the review log; diff against `scratchpad/cp-snap/0081_.../spec-r1-prefix` to see the fix round. — EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../spec-r2 proposals/0081_.../` names only the review log.

MISTAKE (mine, nearly filed): "§15.4's epoch comparison is a TOCTOU: an epochless successor can mint a fresh epoch between the compare and the deregistration, and the hold starts too late to cover that interval." The window is real and reachable (a compensation for an UNSTARTED entry, plus a client `/start` retry). It dies on the staged text's own wording: "performs the slot release and the runtime teardown **only when the two are equal**" (spec-changes.md:660) is a condition on the performing, so an implementation whose entry changed epoch between compare and act is already non-conformant as written. Adding the word "atomically" is clarification, not a defect fix, and this loop has refuted five contract-completeness candidates on that bar. Do not rebuild it.

MISTAKE (mine, nearly filed): "the staged §6.2 prose and §5.2 give the reclaim hold two different end points — §6.2 ends it when the slot leaves `slot_cleanup` (which `slot_cleanup ──→ leaked` does at the cleanup timeout, spec/06:148), §5.2 ends it when the cleanup finishes." The r1 text DID assert the identity ("the `slot_cleanup` sub-state IS exclusive occupancy ... lasting until the cleanup reaches `released` or `leaked`"). The FIX ROUND already closed this: the staged sentence is now one-directional — "While a slot is in `slot_cleanup`, on either incoming edge, the adapter holds the slot's identifier" (spec-changes.md:614) — and asserts nothing about the converse. Credit where due; a lens reading the r1 snapshot will file a defect that no longer exists.

MISTAKE (mine, nearly filed): "§4.7's `ConfigureWorkspace` row says 'Idempotent (same `cwd` path is safe to send twice)' (spec/04:673), while staged §15.4 makes it non-conformant to admit an epochless request onto an identifier whose session has started (spec-changes.md:658) — and `ConfigureWorkspace` sets `started`." Two things kill it: within one attempt the caller echoes its epoch, so the repeat is admitted; and across attempts there is no repeat, because `Binder.Launch`'s `ConfigureWorkspace` failure calls `reclaim()` (failPhase) and the gateway retries on a FRESH pod. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1008-1013,:997-1000.

FACT: the epoch fence is airtight in the direction that matters, and here is the closed argument so nobody re-derives it. A successor may take the identifier only while the incumbent entry's session has NOT started (spec-changes.md:634). Therefore a compensation for an attempt whose start the adapter ADMITTED can never meet a different epoch, so the residue class this whole proposal exists for is always fenced-and-torn-down, never `superseded`. `superseded` is reachable only for a pre-start compensation, where performing nothing is correct.

FACT: the exclusion predicate and the shipped discriminator are one statement after the broadening. §7.1's "did not complete" = "did not answer, or answered `reclaimed` without reporting a clean exit" (spec-changes.md:344), §5.2's `**Max retries:**` says "did not complete" (:484), and the gateway sets `ExcludePod` under `sbe.Leaked || relErr != nil` with `Leaked = err != nil || !cleanly`. `superseded`/`absent` answer `ExitedCleanly: true` under CODE-1's `closeErr == nil && (live || treeErr == nil)` (no close, no tree removal), so a completed refusal is never recorded leaked and never excludes the pod. Verified end to end; do not re-derive.

FACT: the §5.2 hold's bound survives the concurrency-scoping trap. The `**Slot cleanup:**` bullet that states `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` sits under a `maxConcurrentSessions > 1` heading (spec/05:542,:545), but SPEC-3's appended pointer — "That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency" (spec-changes.md:554) — carries the bullet and its timeout across the boundary, and at concurrency 1 the formula degenerates to `max(T/1,5) == T`. The Open at review-log:599 ("Nothing in the staged spec bounds the reclaim hold on an exclusive pool") is answered by that pointer sentence and is a performance-lens framing, not a reliability hole.

UNVERIFIED: the slot-counter consequence of a `superseded` reclaim. §7.1 keeps "releases the slot reservation afterwards" (spec-changes.md:344), so a compensation answered `superseded` still drives a `leaked=false` `ReleaseSlotReservation`, decrementing `active_slots` for an identifier a LIVE successor occupies — the pod can then carry `maxConcurrentSessions + 1`, which is what §5.2 "Slot assignment atomicity" exists to prevent (spec/05:557). I did not file it: the create-time-reserved retry already runs on unaccounted occupancy in the shipped tree (standing Settled entry on `BindReservedSlot`), and the brief assigns capacity math to the performance lens. The epoch does make it strictly more reachable, because the successor now SURVIVES where it used to be torn down. A performance or capacity lens should decide once; this entry supersedes the pre-epoch framing at review-log:550.

WATCHOUT: `pkg/sandbox/slotstate` and §6.2's per-slot sub-states are the GATEWAY's model (Redis occupancy, cleanup-timeout-driven `leaked`), while the reclaim hold is an adapter in-memory map. Any future sentence that equates the two, in either direction, reopens the r1 defect the fix round closed. — EVIDENCE: spec/06:148,:160; spec-changes.md:614.


### [spec.2.review-security.1]

DECISION: returning an EMPTY findings list on the staged spec edits — BECAUSE every security
control my lens owns is untouched by the amendment, and the two candidate angles the epoch and
the hold open are both already adjudicated in the standing context — ALTERNATIVES: I built and
dropped five candidates, each recorded below so nobody rebuilds them.

FACT: the epoch/hold amendment adds NO control-plane, network, RBAC, admission, credential-path
or pod-security surface. The credential path SPEC-3's widened action list names,
`/run/lenny/slots/{sessionId}/` plus `credentials.json`, is exactly what the tree uses.
EVIDENCE: pkg/adapter/slotlayout/slotlayout.go:100-104; spec/13_security-model.md:26,:28,:30;
spec/05_runtime-registry-and-pool-model.md:461,:471.

FACT: the escape hatch holds in the spec, not only in the code. The epochless `Shutdown` is
stated as the unconditional teardown at THREE staged sites, so every non-bind caller (§11.4
revoke fan-out, the occupancy-zero recycle edge, `Binder.ReleaseSlot`) stays defined and no
mandatory teardown is made conditional on an in-pod value. EVIDENCE:
spec-changes.md:276 (§4.7 row), :636 (§4.7.1 caller rules), :660 (§15.4 conformance).

FACT: the epoch precondition does NOT reach the credential-revocation RPCs. `RotateCredentials`,
`RevokeCredentials` and `ExtendCredentialLease` resolve through `slotStateLocked` rather than
`ensureSlotStateLocked`, so they are outside the seven bind-sequence RPCs by construction and
§4.7.1's "every other RPC ... no epoch precondition applies" is true of them. A revoke can
therefore never be fenced off by an epoch mismatch. EVIDENCE: pkg/adapter/slotcreds.go:70-72,
:103-107,:122-127; the seven entry-creating sites are staging.go:134,:181,:337, slotcreds.go:26,
slotsession.go:75 (from session.go:111), resume.go:50, sdkwarm.go:217.

MISTAKE nearly filed, five security dresses, none of which clears the bar:
 (1) "`superseded`/`absent` are pod self-reports that exempt a slot from the leak accounting,
     the §5.2 replacement trigger and the `ExcludePods` exclusion." Dies on standing Settled
     #493's bar: the lie makes the gateway FAIL TO DO something new, never DO something it does
     not do today, and `exited_cleanly: true` already buys the identical exemption.
 (2) "A compromised or buggy adapter can refuse every fenced teardown forever." Same class; the
     threat model is a component that already owns the pod's credential file.
 (3) "The exclusive-pod carve-out returns a recycling `maxConcurrentSessions: 1` pod to idle
     inventory carrying a credential residue, because `ReleaseSlotReservation` hard-codes
     `recycle=false` so `bound → recycling` and its whole-pod scrub are unreachable." Real
     mechanism, and it is the barred close variant of the twice-killed §13.1 sequential-reuse
     family (standing Trap "MISTAKE nearly filed: the §13.1 sequential-reuse credential-isolation
     guarantee is broken"). Pre-existing besides: today a failed bind sends no `Shutdown` at all.
 (4) "The reclaim hold is an unbounded refusal, so an unanswered reclaim denies the identifier
     until pod termination." Availability, explicitly accepted at spec-changes.md:185-187, and the
     hold is per session identifier so no other session can collide with it (Settled #518).
 (5) "The epoch fence is guessable (small monotone integers)." Only a gateway replica can reach
     the adapter's gRPC surface, and a gateway replica can already send the epochless
     unconditional form, so guessing buys nothing.

WATCHOUT: §4.7.1 says of the epoch "It is never derived from ... the `coordination_generation`
the same requests carry ... The two travel on the same messages." Of the seven bind-sequence
REQUEST messages only `ResumeRequest` carries `coordination_generation` (field 14);
`PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`, `RunSetupRequest`,
`AssignCredentialsRequest`, `StartSessionRequest` and `ConfigureWorkspaceRequest` carry none.
`ShutdownRequest` does (field 6). NOT filed, because the sentence is unquantified and survives a
charitable reading over the two messages that do carry both, and nothing an implementor builds
changes. A citations or edit-sites lens may want it; do not spend a security verifier pair on it.
EVIDENCE: schemas/lenny-adapter.proto, the six messages above; spec/10_gateway-internals.md:30.

OPEN (sharpened, and the one thing I would hand to a mechanism lens): the standing Open "Can an
adapter restart produce an epoch collision?" was recorded as "judged closed by §7.1's no-re-dial
rule, since the old connection dies with the process". That ground is weaker than it reads.
`adapterclient.Client` holds a `grpc.ClientConn`, and grpc-go re-establishes the transport
transparently on the next RPC after the server process restarts, so "the connection the failed
attempt already holds" (spec-changes.md:344) is not evidence that the compensation reaches the
SAME adapter process instance. After an adapter container restart the counter restarts at 1, so a
successor that binds on the new process at epoch 1 compares EQUAL to a lagging compensation
holding epoch 1 from the old process. The window is narrow (the compensation is issued
immediately on failure, and grpc backoff plus a whole successor bind must fit inside it), which is
why I did not file it, and it is a mechanism finding rather than a security one. Whoever settles
it should either trace the reconnect or make §7.1's no-re-dial sentence say what actually holds.

USEFUL [Standing context, Settled #493 "the honest security residual"]: it is the entry that
kills the whole in-pod-self-report family for this proposal, and it states the bar precisely
enough ("a path where the lie makes the gateway DO something it does not do today") that three of
my five candidates died on it in one reading. Keep it.
USEFUL [Standing context, Settled #245 and #246]: #245 ("the specification's pod-self-report
posture does not reach `exited_cleanly`") and #246 ("the staged edits touch NO §10.3 / §13.1 /
§13.2 control, verified by absence") are the two entries that let this lens skip re-deriving the
posture sweep and spend the round on the amendment's new text instead.


### [spec.3.fix-G1.1]

DECISION: closed the "how is an epochless `Shutdown` spelled on the wire" defect by defining the vocabulary once per block rather than by rewriting the nine "carrying no epoch"/"epochless" sites — BECAUSE each block already fixes the epoch's value domain in its opening sentence, so one clause there ("The epoch is a positive integer, so zero is not an epoch: a request whose bind epoch is zero carries none, and a response reporting zero reports none.") resolves every later occurrence in that block by reference — ALTERNATIVES: declaring `optional int64 expected_bind_epoch` to make the prose's presence framing true (rejected: `schemas/lenny-adapter.proto` declares `optional` on no field, it would engage 0075's presence rule for the first time, and it pushes a `has` check into every call site and into CONF-1); rewriting all nine sites into explicit zero-value phrasing (rejected as churn that multiplies the chance of an inconsistency).

DECISION: DELETED the §4.1 bind-epoch sentence from SPEC-1 rather than restating it in value terms — BECAUSE §4.1's closing rule is about a field's presence standing in for a scope, and `expected_bind_epoch` is a bare proto3 `int64` with no wire presence, exactly as the `coordination_generation` fence on the same message is; §4.1 says nothing about that field either, so the rule is not engaged and there is nothing for §4.1 to state. §4.1's replacement sentence now delegates both teardown preconditions to §4.7. ALTERNATIVES: the reviewer's literal suggestion (keep the sentence, restate it as "carries a bind epoch on some messages and a zero on others") was rejected as the same failed answer one step smaller: it leaves a second statement of the epoch in the subsection least entitled to own it.

FACT: `ShutdownRequest` holds only scalars plus `RecycleScrub recycle = 5`; `coordination_generation` is a bare `int64 = 6` and is a precondition, exactly parallel to the staged `expected_bind_epoch = 7`. EVIDENCE: schemas/lenny-adapter.proto:1609-1636, and the generation's comment at :1630-1635.

WATCHOUT: the Kubernetes `DeleteOptions` analogy that standing context #514 adjudicated as below the bar is now GONE, because the sentence carrying it was deleted. Do not re-derive the analogy's error semantics; there is no analogy left in the staged text. EVIDENCE: spec-changes.md, SPEC-1 §4.1 "Replace it with" block.

WATCHOUT: deleting the §4.1 epoch sentence falsified the SPEC-5 §4.7.1 preamble, which claimed "the §4.1 paragraph, the §4.7 `Shutdown` row, and §7.1 each point at one statement rather than carrying three". It now names the §4.7 row and §7.1 only, and states why §4.1 names nothing. A later edit that reintroduces an epoch mention in §4.1 has to move that preamble back. EVIDENCE: spec-changes.md, SPEC-5 §4.7.1 preamble paragraph.

FACT: the §4.7.1 block keeps its own "a request carrying one and a request carrying none address the same session and ask for the same thing" sentence. That is deliberate: inside §4.7.1 the phrase now resolves against the zero convention the block's first paragraph states, and §4.7.1 is the section that owns the epoch. EVIDENCE: spec-changes.md, §4.7.1 admission-rules paragraph.

CORRECTS [Standing context, the UNVERIFIED entry "How is 'a `Shutdown` carrying no epoch' spelled on the wire?"]: it is now stated rather than inferable. Both SPEC-5 blocks state that zero is not an epoch, and §15.4 adds the missing non-conformance case: an adapter that compares a zero bind epoch against the entry it holds, or answers `superseded` for one, does not conform. That clause is what keeps the §11.4 revoke fan-out, the occupancy-zero recycle edge, and `Binder.ReleaseSlot` defined against a conforming third-party adapter.

USEFUL [Standing context #322]: "Adding `bind_epoch` to the seven bind REQUESTS raises no §4.1 obligation ... only the `ShutdownRequest` paragraph needs the staged precondition sentence" saved me from widening the §4.1 edit. Note it is now half wrong: the `ShutdownRequest` paragraph needs no precondition sentence either, for the presence reason above.


### [spec.3.fix-design-G1.1]

DECISION: Give the "epochless" vocabulary ONE wire definition per block (zero is not an epoch; a request whose bind epoch is zero carries none) in the §4.7.1 opening paragraph and the §15.4 bind-epoch-contract opening paragraph, add the missing §15.4 non-conformance clause for a zero request epoch, and DELETE SPEC-1's added §4.1 epoch sentence outright rather than rewording it — BECAUSE `expected_bind_epoch` is a bare proto3 scalar with no wire presence, so §4.1's rule ("no operation is selected by a field's presence standing in for a scope") is not engaged by it at all, and the same message already carries a bare-scalar precondition, `coordination_generation` (schemas/lenny-adapter.proto:1630-1635), that §4.1's paragraph says nothing about. ALTERNATIVES: (a) make the field proto3 `optional` so presence is real — rejected, the file uses `optional` on no field, it would make 0075's presence rule genuinely engaged instead of moot, and it forces a `has` check into every call site; (b) the reviewer's literal suggestion, keep the §4.1 sentence and restate it in value terms — rejected, that is the same answer one step smaller at a location that has already failed two rewrites; (c) restate the zero convention at each of the nine "carrying no epoch"/"epochless" sites — rejected as hair.

FACT: `ShutdownRequest` holds `coordination_generation` as a plain `int64` (a precondition, not a scope selector) and §4.1's `Request Message Scope` paragraph never mentions it. That precedent is the whole argument for §4.1 saying nothing about the bind epoch either. EVIDENCE: schemas/lenny-adapter.proto:1630-1635; spec/04_system-components.md:149-157.

FACT: §15.4 names zero only for RESPONSES ("An adapter that reports zero on a bind-sequence response does not conform"), never for requests, while CODE-1 branches on `epoch != 0` and CONF-1 asserts "a zero epoch is the unconditional teardown". A third-party adapter built from §15.4 alone would answer `superseded` for every ordinary session end. EVIDENCE: spec-changes.md:661,665; non-spec-changes.md:144,990.

WATCHOUT: the §4.7 `Shutdown` row is already the longest block in the proposal and it links §4.7.1 twice. Do NOT add the zero definition there; §4.7.1 is a subsection of §4.7, so the vocabulary defined there resolves for the row. EVIDENCE: spec-changes.md:279.

WATCHOUT: deleting the §4.1 epoch sentence cascades to three prose sites that assert it exists — spec-changes.md:123-128 (Design narrative repeating the §4.1 justification), summary.md:524 (the 0075 impacts row) and summary.md:531 (the SPEC-1 one-liner). All three must change in the same edit or the proposal claims a sentence it no longer stages.

WATCHOUT: `Shutdown with the field unset` at non-spec-changes.md:1380 describes a tier-3 case that cannot be written as stated, because a non-optional scalar has no unset. It is the same defect at a parallel site and is in scope for this edit.

OPEN: implementation-checklist.md:32 (S14) still says the epoch is "minted per entry", which the standing context records as refuted (it is minted per admitted bind ATTEMPT). Outside this finding; a later round should file it.


### [spec.3.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens on the spec staging — BECAUSE every one of the fourteen staged anchors matches the tree verbatim and uniquely, every cross-reference target already exists as a heading, and no staged edit references an artifact a later spec sub-step creates — ALTERNATIVES: rejected filing the "cleanup running" vs "deregistration" phrasing drift between §4.7.1/§15.4's admission rule and §5.2/§6.2's hold window (§15.4's own reclaim-hold block defines the window at deregistration two sentences before its non-conformance clause says "whose cleanup is still running", and §4.7.1 cites §5.2 for the window, so the reader has the answer in both places — this is the same wording-precision class the material skeptic already refuted four times in this window); rejected filing the §5.2 "Fresh workspace guarantee" against the epochless-retry-inherits-the-abandoned-entry case, because that guarantee is scoped to the `maxConcurrentSessions > 1` slot retry policy which always assigns a NEW slot, and the inheritance case only arises on the create-time-reserved path the policy does not place.

FACT: all fourteen anchors verified verbatim and unique at these lines — spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown` row opening), spec/04:695 (§4.7.2 heading, the SPEC-5 insertion boundary), spec/04:848 (§4.7.9 step 5 is at :852ff under it), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**` pod-selection sentence), spec/06:152-153 (`receiving_uploads ──→ running`), spec/06:156-158 (fence close / `**\`reserved\` hold semantics.**`), spec/06:234 (mid-resume cancel clause), spec/07:23-24 (atomicity paragraph and its continuation line), spec/07:210/:213/:214 (§7.2 preamble, step 2, step 3), spec/07:414 (§7.3 list item 4), spec/15:1469-1471 (SDK-warm demotion contract / §15.4.1 heading), spec/29:710 (step 13's `§15.4.3` sentence ending). — EVIDENCE: spec/04_system-components.md:157,686,695; spec/05_runtime-registry-and-pool-model.md:453,545,555; spec/06_warm-pod-model.md:152,156,234; spec/07_session-lifecycle.md:23,210,214,414; spec/15_external-api-surface.md:1469; spec/29_communication-scenarios.md:710

FACT: the §7.1 insertion point is INSIDE a fenced ``` block (the flow listing opens at spec/07:5 and the atomicity paragraph at :23 sits within it), so the new paragraph's markdown links will not render as links. That is the shipped convention there — the atomicity paragraph already carries `[§6.2](06_warm-pod-model.md#62-pod-state-machine)` inside the same fence — so it is not a defect, and a future agent should not "fix" it. — EVIDENCE: spec/07_session-lifecycle.md:5,23

FACT: no existing gate hard-fails between S4 (spec edge) and S6/S7 (docs table, `ValidTransitions`). The only tier-11 test that reads the §6.2 per-slot fence is `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency`, which asserts containment of four named general edges and absence of those four from the concurrent-occupancy block; a fifth general edge passes it untouched. Nothing in tests/ or pkg/ compares the fence's edge set exhaustively against `pkg/sandbox/slotstate`. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-73

FACT: the new §4.7.1 sentence "Where both appear on one message, as on `Resume` and `Shutdown`" is accurate against the wire. `coordination_generation` is declared on exactly twelve request messages, and of the seven bind-sequence RPCs only `ResumeRequest` carries it; `ShutdownRequest` carries it too. — EVIDENCE: schemas/lenny-adapter.proto:1405 (ResumeRequest), :1635 (ShutdownRequest)

FACT: the new §4.7.1 caller rule "`DemoteSDK` ... removes the entry" is true against the tree. `Server.DemoteSDK` calls `releaseSessionSlot(sessionID)`, which calls `deregisterSlot` and then `removeSlotTree`, synchronously before the RPC returns. — EVIDENCE: pkg/adapter/sdkwarm.go:295-297; pkg/adapter/slotsession.go:214-219

UNVERIFIED: because `DemoteSDK`'s deregistration+tree removal IS a cleanup in the sense §5.2's reclaim hold defines, the ConfigureWorkspace-failure fallback re-binds the SAME slot identifier immediately afterwards. It is safe only because `releaseSessionSlot` completes inline before `DemoteSDK` returns; the spec states no such ordering. Nobody has checked whether a conforming third-party adapter that runs its demotion cleanup asynchronously would refuse the mandated pod-warm fallback on the reclaim hold. The material or wire-contract lens should decide whether §15.4's reclaim-hold block owes a sentence on this. — EVIDENCE: pkg/adapter/sdkwarm.go:274-301; proposals/0081_.../spec-changes.md:641 (the caller rule), :671 (the §15.4 hold block)

FACT: §28 carries no register row naming `Shutdown` (grep over spec/28_communication-channels.md returns nothing), so the proposal's "deliberately untouched" claim for §28's registers holds. — EVIDENCE: spec/28_communication-channels.md (no match)

WATCHOUT: `diff -ru` between the round-3 snapshot dir and the live proposal returns EMPTY, and so does `spec-r3-start` vs `spec-r3`. The useful diff for "what the last fix round changed" is `spec-r2` vs `spec-r3` under scratchpad/cp-snap/0081_.../. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/


### [spec.3.review-citations.1]

FACT: the r3 snapshot is byte-identical to the live proposal again; the real spec delta is
`spec-r2` → live and it is ~7 hunks: the caller-echo rewrite (spec-changes.md:114-117), the
§6.2-hold rationale swap (:143-145), §7.1's "the bind epoch that attempt holds" (:347), the
SPEC-4 §6.2 prose rewrite and its rationale (:617,:620-624), the §4.7.1
`coordination_generation`/`Shutdown`/`DemoteSDK` rewrites (:635,:637,:641), the §15.4
`Shutdown` carve-out (:661), and one "Spec files touched" line. EVIDENCE:
scratchpad/cp-snap/0081_.../spec-r2 vs proposals/0081_....spec-changes.md

FACT: the mechanical anchor sweep re-run clean on the whole staging this round — all 12
"text to replace" fenced blocks resolve to exactly one site in spec/ and all 19
replacement/insertion blocks to zero. Do not re-run it unless spec/ moves. EVIDENCE: the
python script form recorded in the standing context Settled entry on the staged-anchor sweep.

FACT: `coordination_generation` and a bind epoch overlap on exactly two messages,
`ResumeRequest` (schemas/lenny-adapter.proto:1405) and `ShutdownRequest` (:1635). No other
bind-sequence request carries a generation. The §4.7.1 sentence "as on `Resume` and
`Shutdown`" is exact; the pre-amendment "the two travel on the same messages" was the false
form. EVIDENCE: schemas/lenny-adapter.proto:976,1002,1053,1077,1098,1121,1179,1312,1400,1543,1588,1630

FACT: the shipped gateway calls `DemoteSDK` in `Binder.Prepare` at
pkg/gateway/podlifecycle/podsession/binder.go:882, BEFORE `stageWorkspace` (:916),
`FinalizeWorkspace` (:921) and `RunSetup` (:933). `Binder.Launch`'s `ConfigureWorkspace`
failure does NOT call `DemoteSDK`; it calls `reclaim()` (:1009-1013). So on the shipped
tree a demotion always precedes every bind-sequence RPC of its attempt and the caller holds
no epoch at that moment. §4.7.1's `DemoteSDK` clause (spec-changes.md:641) is therefore
fail-safe rather than load-bearing today, and the adapter half of it is true:
`Server.DemoteSDK` does `releaseSessionSlot(s.anyRegisteredSession())`
(pkg/adapter/sdkwarm.go:296-298). Do not file the clause as unsupported.

WATCHOUT: `int64 expected_bind_epoch` is a proto3 SCALAR, so it has no presence; the
sibling `RecycleScrub recycle = 5` in the same message IS a message field and its comment
legitimately says "Absent on the terminate path". Any prose that says the epoch is
"present on some messages and absent on others" is using presence language the wire does
not provide. EVIDENCE: schemas/lenny-adapter.proto:1609-1636; spec-changes.md:248-249

MISTAKE (this round's fix): the SPEC-4 §6.2 rewrite removed one restatement of the hold's
window and wrote another in the same sentence. spec-changes.md:617 now reads "[§5.2] states
the reclaim hold that refuses every one of them, from the adapter's deregistration of the
slot's registry entry until the cleanup finishes", while its own rationale three lines below
says "§6.2 does not become a third statement of a window §5.2 and §15.4 already fix"
(:620-624) and the Design paragraph says "§6.2 points at the hold rather than restating its
window" (:143-145). NOT FILED: the three windows agree verbatim, so the applied spec is
consistent and only the rationale is inaccurate; it fails the materiality bar the material
skeptic has applied to five sibling rationale findings this loop. A fixer already editing
that paragraph should drop the trailing window clause or correct the two rationale
sentences.

UNVERIFIED: spec-changes.md:143-144 asserts "§6.2's `slot_cleanup` sub-state begins when the
gateway observes the failure". Nothing in §6.2 states a tracker or a start instant for the
sub-state, and spec/05:544 attributes the sibling `failed` marking to the ADAPTER ("the
adapter marks that `slotId` as `failed`"). The claim is the whole ground for the r3 §6.2
rewrite. Somebody should settle who owns the per-slot sub-state before the next edit rests
on it. Not filed as a wrong-component attribution because §6.2 itself is silent and
`pkg/sandbox/slotstate` is gateway-side.

DECISION: filed exactly one finding, the missing wire spelling of "a `Shutdown` carrying no
epoch" — BECAUSE it is the one gap whose absence breaks a shipped caller against a
third-party adapter built from §15.4 alone (`Shutdown`/`ShutdownRecycle` pass zero at every
existing call site, non-spec-changes.md:966-968), rather than a completeness preference.
ALTERNATIVES rejected: the §6.2 window restatement above; the §4.1 "present/absent"
sentence on its own (rationale prose, folded into the filed finding as its second
evidence leg); "the generation is validated on the RPCs that carry it" against spec/10:30's
"every gateway→pod RPC" (the staged form is a narrowing of a rule that is already
impossible as written and already unimplemented, log entry 337).

USEFUL [standing-context Settled #317, #321, #324]: the seven-RPC bind-sequence set, the
free field numbers, and "`Shutdown` appears in no §28 register row" each saved a full
re-derivation this round and each re-checked clean.


### [spec.3.review-client-surface.3]

DECISION: returned an EMPTY findings list for the client-facing-surface lens on the staged spec edits — BECAUSE every parallel representation of every client-facing name the staging touches was checked and matched, and the two candidates that remained are both close variants of findings this loop has already refuted — ALTERNATIVES: I nearly filed the wire-spelling gap (see WATCHOUT below) and the `concurrent_slots_exhausted` definition drift, and declined both on the recorded precedent.

FACT: the spec-changes.md file is byte-identical to the round-3 snapshot. `diff -rq scratchpad/cp-snap/.../spec-r3 proposals/0081_.../` exits 0 across the WHOLE proposal directory, so there is no "what changed since last round" to read hardest. A future round should run that diff first and not budget time for a changed-text pass that does not exist. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r3

FACT: every anchor and verbatim replace-target in the staged spec edits resolves. Checked by reading the tree, not the log: spec/04_system-components.md:657 (`### 4.7`), :659 (`#### 4.7.1`), :848 (`#### 4.7.9`), :1099 (`### 4.9`); spec/15_external-api-surface.md:1458 (`### 15.4`), :1686 (`#### 15.4.2`), :1707 (`#### 15.4.3`), :614 (`### 15.1`); spec/10_gateway-internals.md:3; spec/06_warm-pod-model.md:78; spec/07_session-lifecycle.md:3 and :378; spec/05_runtime-registry-and-pool-model.md:365. SPEC-1's §4.1 replace-target matches spec/04_system-components.md:157 verbatim and SPEC-3's two §5.2 targets match spec/05_runtime-registry-and-pool-model.md:545 and the `**Scrub model.**` paragraph verbatim. Do not re-run this sweep.

FACT: the epoch/outcome vocabulary is consistent across its two staged homes. §4.7's `Shutdown` row (spec-changes.md:242) and §15.4's bind-epoch block (:618-624) give the same seven-RPC bind-sequence list, the same two admission rules, the same three outcome names with the same definitions, and the same process-scoped minting rule. There is no third spec home. The proto enum `SLOT_RECLAIM_OUTCOME_{RECLAIMED,ABSENT,SUPERSEDED}` (non-spec-changes.md:1011-1025) matches the prose lowercase names on the `SESSION_SCRUB_OUTCOME_*` precedent.

FACT: `ResumeRequest` does carry `coordination_generation` (schemas/lenny-adapter.proto:1405), so §4.7.1's "Where both appear on one message, as on `Resume` and `Shutdown`" is accurate. `ShutdownRequest.coordination_generation` is field 6 (:1635) and `ShutdownResponse` is `{exited_cleanly=1, exit_code=2}` (:1665-1668), so SCHEMA-1's `slot_reclaim = 3` and `expected_bind_epoch = 7` are free.

CORRECTS [standing context, the SCHEMA-1 `AssignCredentialsResponse` MISTAKE entry]: that entry says "`AssignCredentialsResponse` ... is not [empty]: it holds `session_id = 1`, `leases = 2`, `rotation_trigger = 3`, `coordination_generation = 5`, with 4 reserved". Those are the fields of `RotateCredentialsRequest` (schemas/lenny-adapter.proto:1035-1058). `AssignCredentialsResponse` IS an empty message at schemas/lenny-adapter.proto:1033 (`message AssignCredentialsResponse {}`), so SCHEMA-1's `bind_epoch = 1` there is correct and the "hard field-number collision `buf` would reject" claim is false. A future round acting on that entry would renumber a correct field.

FACT: `DemoteSDK`'s `releaseSessionSlot` is SYNCHRONOUS inside the RPC (pkg/adapter/sdkwarm.go:296-301 → pkg/adapter/slotsession.go:214-220, `deregisterSlot` then `removeSlotTree` then return), so in the first-party adapter the reclaim hold the demotion starts is already over when `DemoteSDK` answers. That is why §4.7.1's "the pod-warm bind sequence that follows a demotion is epochless and takes the identifier at a fresh epoch" does not collide with the hold. The collision is only reachable for a third-party adapter that cleans up asynchronously, which is why I did not file it.

WATCHOUT: `WARM_POOL_EXHAUSTED` / `details.reason: "concurrent_slots_exhausted"` is defined in the untouched spec/05_runtime-registry-and-pool-model.md:549 as meaning "pods exist but all slots are full". SPEC-2's `**Max retries:**` widening lets that reason be returned when the only candidate pod was excluded for holding an incomplete reclaim rather than for being full, and the staging says the definition "is not edited". I declined it because the shipped tree already returns that reason for `ErrTenantMismatch` (pkg/gateway/sessionserver/podclaimerror_internal_test.go:88), so the change widens pre-existing looseness — the class this loop has declined repeatedly. Do not re-derive it as new.

OPEN: how "a `Shutdown` carrying no epoch" is spelled on the wire is still stated nowhere in the staged spec. `expected_bind_epoch` is a non-optional proto3 `int64`, so absence is zero, and the code lane fixes zero as the unconditional form (non-spec-changes.md:138-141, :990). §15.4 says only that the epoch is "a positive integer" and that a no-epoch `Shutdown` "is compared against nothing" (spec-changes.md:618,:624). A third-party adapter reading §15.4 alone can read a zero-valued field as "epoch 0", which no entry carries, and answer `superseded` to every unfenced caller — the §11.4 revoke, the recycle edge, and `Binder.ReleaseSlot`. I did NOT file it: three §15.4-completeness findings have already been refuted this loop on the ground that a semantically-stated requirement plus a correct first-party implementation is not a correctness defect, and this one would meet the same refutation. It duplicates standing-context OPEN "How is a `Shutdown` carrying no epoch spelled on the wire?". Whoever settles that OPEN should settle it in §15.4, not re-file it as a review finding.

FACT: the amendment reaches no client-facing surface outside the gateway↔adapter proto. Verified by absence across the tree: `grep -rn "exited_cleanly\|expected_bind_epoch\|bind_epoch\|SlotReclaim\|slot_reclaim" spec/ docs/ schemas/ sdks/ pkg/gen` returns exactly one hit, schemas/lenny-adapter.proto:1666. No REST field, no OpenAPI operation, no MCP tool schema, no CRD, no runtime or client SDK type, and no JSONL or runtime-ops-events frame carries the epoch or the outcome, so there is no parallel client representation left un-mirrored. The staged design also mints no client-visible error code or reason value.


### [spec.3.review-docs-alignment.4]

DECISION: returned an EMPTY findings list — BECAUSE this loop's scope admits only findings
whose remedy lands in `.spec-changes.md`, and every docs-alignment defect I could substantiate
has its remedy in `docs/` (out of scope, and already carried as standing Deferred entries for
`docs/reference/adapter-contract.md:75` and `docs/reference/state-machines.md:236`).
ALTERNATIVES: I built and dropped one non-lens finding, recorded below as UNVERIFIED so the
next round does not rebuild it from scratch.

CORRECTS [review-log.md Standing context, the `### Traps` entry beginning "MISTAKE: SCHEMA-1
asserts \"`AssignCredentialsResponse` is an empty message today, so its first field is 1\""]:
that Traps entry is itself wrong, and acting on it would BREAK a correct staging. The tree has
`message AssignCredentialsResponse {}` — a genuinely empty message. The field set the entry
quotes (`session_id = 1`, `leases = 2`, `rotation_trigger = 3`, `coordination_generation = 5`,
`4` reserved) belongs to `RotateCredentialsRequest`, which is the next message in the file.
SCHEMA-1's `bind_epoch = 1` on `AssignCredentialsResponse`
(non-spec-changes.md:1040,:1044) is CORRECT and must not be renumbered to 6. The same Traps
entry appears in the review log's Open section ("`AssignCredentialsResponse.bind_epoch = 1` is a
hard field-number collision — OPEN and not yet filed"); that Open must be closed as refuted
rather than acted on. EVIDENCE: `awk '/^message AssignCredentials(Request|Response)/,/^}/'
schemas/lenny-adapter.proto` shows `message AssignCredentialsResponse {}` immediately followed
by `message RotateCredentialsRequest { SessionId session_id = 1; ... int64
coordination_generation = 5; }`; the `reserved 4; reserved "slot_id";` the entry cites is
`RotateCredentialsRequest`'s.

FACT: only two of the fourteen `coordination_generation` field declarations in
`schemas/lenny-adapter.proto` sit on a message the bind epoch also touches — `ResumeRequest`
(field 14) and `ShutdownRequest` (field 6). The staged §4.7.1 sentence "Where both appear on
one message, as on `Resume` and `Shutdown`, each is checked on its own terms"
(spec-changes.md:635) is therefore an exhaustive enumeration, not an illustrative one, and it
is accurate. No bind-sequence RESPONSE carries a generation. EVIDENCE:
schemas/lenny-adapter.proto:981,1009,1058,1082,1103,1126,1186,1317,1405,1460,1492,1548,1593,1635
(the declarations) against the message each falls in.

FACT: the newest §4.7.1 `DemoteSDK` caller clause checks out against the tree. `DemoteSDK`
really does remove the registry entry: after clearing `sdkConnected` it calls
`noteRuntimeClosed` and `releaseSessionSlot` on `anyRegisteredSession()`. EVIDENCE:
pkg/adapter/sdkwarm.go:286-302. §4.7's own `DemoteSDK` row (spec/04:674) says only "return the
pod to pod-warm state" and says nothing about an entry, so the citation carries the first
clause rather than the second; judged below the bar rather than a false citation.

UNVERIFIED: the staged reclaim hold is written as an obligation on EVERY adapter
deregistration-plus-cleanup, while CODE-1/CODE-6 take it only inside `Shutdown`. §5.2's staged
paragraph reads "The adapter holds a slot's identifier from the critical section that
deregisters the slot's registry entry until the cleanup that reclaims it has finished"
(spec-changes.md:559) and §15.4's companion adds the conformance clause "An adapter that admits
a bind onto an identifier whose cleanup is still running does not conform" (:671). The staged
hold is inserted in exactly one place, `Shutdown`'s clause two
(`if removed { s.holdReclaimLocked(sessionID); defer s.releaseReclaim(sessionID) }`,
non-spec-changes.md:185-188). Sixteen production sites deregister-and-remove-the-tree outside
`Shutdown` through `releaseSessionSlot`, which does `deregisterSlot` then `removeSlotTree`
with `s.mu` released between them: pkg/adapter/slotsession.go:214-220, called from
session.go:133,:147,:157; resume.go:69,:73,:89,:107,:126,:134,:141; sdkwarm.go:236,:241,:251,:298.
I did NOT file it, on the precedent the Standing context already sets for this exact shape:
"The adapter implements no process-group kill ... so §5.2's action list is ahead of the code on
that clause before and after SPEC-3. Pre-existing; do not re-derive it as a finding." The hold
would also not close the hazard at those sites, because `releaseSessionSlot` deregisters by
identifier with no identity check, so it deletes a successor's entry whether or not the
identifier is held — which is standing Deferred [pkg/adapter/session.go, pkg/adapter/resume.go,
a later proposal], the identity-checked-deregister class fix. Who should settle it: whoever
adjudicates whether §5.2's "the cleanup that reclaims the slot" is scoped to the cleanup a
`Shutdown` runs, or reaches every pre-start rollback release. A legal spec-side answer exists
(scope the hold's antecedent to the `Shutdown` slot release), so it is decidable inside the
spec lane if anyone wants it decided.

FACT: `examples/runtimes/echo/` does not exist in the tree at all, so the standing Open "Is
`examples/runtimes/echo/` an adapter rather than a runtime binary?" resolves as moot for this
proposal: spec/15:1466's "A Go reference implementation of the adapter (`examples/runtimes/echo/`)"
names a path with no directory behind it. Pre-existing spec fiction, not 0081's, and it cannot
be an unstaged edit site because there is nothing there to edit. EVIDENCE: `ls examples/runtimes/`
returns nothing; spec/15_external-api-surface.md:1466.

WATCHOUT: the diff the brief hands you is empty against `spec-r3`. The newest real delta is
`spec-r2` → live, and it is 90 diff lines: the §4.7.1 caller-rule rewrite (the caller HOLDS an
epoch rather than echoing the most recent one observed, plus the `DemoteSDK` clause), the
`Shutdown`-is-not-a-bind-sequence-request sentence added to both SPEC-5 blocks, the §6.2 prose
paragraph's closing sentences re-pointed at §4.7.1 and §5.2, the design narrative's
`slot_cleanup`-begins-earlier sentence, and one line of "Spec files touched". EVIDENCE:
`diff -u scratchpad/cp-snap/0081_.../spec-r2/....spec-changes.md proposals/0081_.../....spec-changes.md`.


### [spec.3.review-feasibility.1]

DECISION: returned an EMPTY findings list — BECAUSE every action the staged spec assigns is
performed by a component that exists under that name and can see the data its check needs, and
the three candidates I built each died on evidence already in the tree or on a standing
refutation — ALTERNATIVES: I considered filing (a) the §5.2 reclaim-hold bound being sourced from
a `maxConcurrentSessions > 1`-scoped bullet, (b) §4.7.1/§15.4 calling the `Shutdown` epoch "a
precondition on the whole request" while the §4.7 row fences only the slot release and the
runtime teardown and says nothing about the whole-pod scrub, and (c) §7.1's "a further attempt …
meets that hold … and spends one of the attempts the retry policy allows" naming a retry path the
compensation's own synchronous sequencing makes unreachable. Reasons for dropping each are below.

FACT: the r2→live delta on `.spec-changes.md` is SMALL and entirely inside the epoch text: the
Design caller-holds rule (:114-117), the Design §6.2-window sentence (:143-145), §7.1's
"the bind epoch that attempt holds" (:347), SPEC-4's prose closing sentences (:617-624), the
§4.7.1 block (`coordination_generation` wording, the `Shutdown`-carries-an-epoch sentence, the
`DemoteSDK` caller rule), the §15.4 block (same two moves) and one "Spec files touched" line.
`spec-r3` and `spec-r3-start` are byte-identical to the live proposal, as the standing trap
predicts. EVIDENCE: `diff -rq scratchpad/cp-snap/.../spec-r2 proposals/0081_.../`

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's new caller rule is true
against the tree. `Server.DemoteSDK` clears `sdkConnected`, then `noteRuntimeClosed` +
`releaseSessionSlot` on `anyRegisteredSession()`. EVIDENCE: pkg/adapter/sdkwarm.go:274-302,
:305-315.

FACT: `ResumeRequest` (field 14) and `ShutdownRequest` (field 6) are the ONLY two epoch-carrying
messages that also carry `coordination_generation`, so §4.7.1's replacement clause "as on `Resume`
and `Shutdown`" is exactly right. The other six bind-sequence requests carry none.
EVIDENCE: schemas/lenny-adapter.proto:1405,:1635; the full `coordination_generation` site list is
SendMessage/Attach/RotateCredentials/ExtendCredentialLease/RevokeCredentials/Interrupt/Checkpoint/
SignalDeadline/Resume/ExportPaths/ReportUsage/Shutdown + CoordinatorFence + CheckpointBarrier.

FACT: §4.7.1's subordinate clause "the generation … is validated on the RPCs that carry it" does
NOT contradict spec/10. spec/10_gateway-internals.md:30 reads "Pods validate the generation on
every gateway→pod RPC", which is already impossible for the RPCs whose messages carry no such
field; the staged clause narrows toward feasibility rather than away from it. Do not file it as
predicate drift.

FACT: only `assignCredentialsSlot` among the credential RPCs goes through
`ensureSlotStateLocked`; `rotateCredentialsSlot`, `extendCredentialLeaseSlot` and
`revokeCredentialsSlot` use the lookup-only `slotStateLocked`. That is what makes §4.7.1's
"every other RPC … neither carries nor reports a bind epoch" implementable at the one seam the
hold and the epoch check land on. EVIDENCE: pkg/adapter/slotcreds.go:26,:67,:102,:122;
pkg/adapter/slot.go:105,:130,:140.

FACT: the adapter is the component that arms the §4.9 direct-delivery-mode expiry timer, so
SPEC-3's "cancels the §4.9 direct-delivery-mode lease-expiry timers armed for that session"
assigns the cancel to the actor that owns the timer. EVIDENCE: spec/04_system-components.md:1169
("In direct delivery mode, the adapter MUST set a local timer for each credential lease's
`expiresAt`").

FACT: no §28 register row and no §28.5.1 contract card names any of the seven bind-sequence RPCs
or `Shutdown`, so SCHEMA-1 widening from one message to eight still adds no §28 row and the
`## Spec sections deliberately untouched` §28 bullet holds. EVIDENCE: `grep -n
"PrepareWorkspace\|StartSession\|AssignCredentials\|ConfigureWorkspace\|RunSetup\|
FinalizeWorkspace" spec/28_communication-channels.md` returns only :1124, :1404, :1411, :1819,
all prose about `Resume`/`AssignCredentials` in the CH-RUNTIMEOPS degradation table.

FACT: spec/18 is still clean. Its only adapter-conformance anchors are Phase 2 (§18.6:121-122,
the `echo` Basic-level reference runtime and `cmd/lenny-compliance`) and Phase 12c (:529-533,
the per-session slot cleanup and whole-pod scrub split). Neither enumerates an admission rule, an
RPC field set, or a §15.4 obligation, so SPEC-5 makes no phase deliverable depend on a later
phase's artifact. This extends the standing Settled entry (which was written before SPEC-5
existed) to the epoch text.

WATCHOUT: the SHIPPED `DemoteSDK` call site is NOT the one §4.7:673 describes. §4.7:673 says the
gateway calls `DemoteSDK` on a `ConfigureWorkspace` FAILURE and falls back to pod-warm; the tree
calls it inside `Binder.Prepare`, BEFORE any workspace RPC, driven by
`sdkwarm.RequiresDemotion(plan)`, and a `ConfigureWorkspace` failure in `Binder.Launch` just
reclaims and returns. That divergence is pre-existing and outside 0081, but it matters for anyone
judging §4.7.1's `DemoteSDK` caller rule: in the shipped ordering the demotion precedes every
bind-sequence RPC on that connection, so the caller holds no epoch to drop and the rule is
vacuous there. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:878-897 (Prepare),
:1008-1013 (Launch); spec/04_system-components.md:673.

FACT (and the reason one candidate died): the EXCLUSIVE bind sequence spans TWO adapter
connections. `Binder.Prepare` dials, runs `PrepareWorkspace`/`FinalizeWorkspace`/`RunSetup`/
`AssignCredentials` and closes; `Binder.Launch` calls `b.reconnect` and sends
`StartSession`/`ConfigureWorkspace` on a fresh one. Under a per-connection epoch latch that
`StartSession` is necessarily EPOCHLESS. Nothing breaks: rule 1 admits an epochless request onto
an existing entry whose session has not started, so Launch re-mints and owns the identifier, and
Prepare's own compensation (sent before Launch exists) still matches. But a later round reasoning
about "one attempt, one epoch" on the exclusive path must know the attempt is two connections, and
must not conclude the caller rule is unsatisfiable there. EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:976-1013.

WATCHOUT (candidate I dropped, with the reason, so nobody rebuilds it): §4.7.1:637 and §15.4:661
both call the `Shutdown` epoch "the precondition on the whole request", while the staged §4.7 row
at :279 fences only two of the row's three operations ("performs the slot release and the runtime
teardown only if … and performs neither when it does not") and says nothing about the whole-pod
scrub the recycle disposition drives. The combination is unreachable — only the fenced
compensation sends an epoch and it never sets the recycle disposition — so it fails the
materiality bar. If a fixer ever widens who sends an epoch, this becomes live.

WATCHOUT (second candidate I dropped): §7.1's ":347" sentence says a further attempt "meets that
hold while the reclaim runs and is refused as a transient condition that spends one of the
attempts the retry policy allows", and the very next clause says §5.2 "keeps the retries its slot
retry policy places off that pod". Within one client request the compensation is synchronous
(`compensateFailedSlotBind` blocks on `cl.Shutdown` inside `materializeSlot`'s wrapper, and
`applySlotRetryPolicy` only sees the error afterwards), so a policy-placed retry can never meet a
hold that is still running: either the reclaim completed (hold released) or it did not complete
(`ExcludePods` fires). The reachable case is the create-time-reserved CLIENT retry, which the
policy does not place — and the edge-case bullet at :181-194 states that correctly. I judged the
§7.1 clause imprecise rather than wrong, because the retry-budget spend is real on the client-retry
route and §7.1's sentence is about the hold rather than about which layer places the attempt.
A round that wants it must first prove the reclaim is always sequenced before the policy retry.

USEFUL [Standing context / Traps]: the "snapshot the brief names is byte-identical" trap and the
"`*spec-changes.md` glob matches both files" trap each saved a wasted step. The Settled entry on
`ensureSlotPaths` running at the top of three staging handlers, and the one on each bind attempt
dialling its own connection, are what let me evaluate the epoch caller rules without re-reading
the whole adapter.


### [spec.3.review-fresh.1]

DECISION: returned an EMPTY findings list on the staged spec edits — BECAUSE every candidate I
built either resolves against the tree, is a close variant of an item the refuted list already
kills, or has its remedy outside the spec lane — ALTERNATIVES: the four I priced and dropped are
written out below so nobody rebuilds them.

FACT: `scratchpad/cp-snap/.../spec-r3` and `-r3-start` are BYTE-IDENTICAL to the live proposal;
the newest real delta is `spec-r2` → live. `diff -rq` over the whole snapshot directory first,
then diff against `spec-r2`. The r2→r3 delta is exactly five hunks and all five are epoch text:
the caller-holds-vs-observes rewrite (spec-changes.md:114-117), the §6.2 hold-window re-point
(:143-145 and the SPEC-4 block at :617-624), the §7.1 epoch-citation change from §4.7 to §4.7.1
(:347), the `coordination_generation` co-travel sentence rewrite (:635), and the `Shutdown`
carve-out added to both epoch blocks (:637, :661). EVIDENCE: spec-changes.md:114,143,347,635,637,661

FACT: the anchor sweep is clean under the r3 text and I re-ran it mechanically. All 31 fenced
blocks in `.spec-changes.md`: every "text to replace" block occurs exactly once across `spec/*.md`
(indices 0,2,5,8,10,12,14,16,18,20,22,24) and every replacement/insertion occurs zero times.
Ten-line python over `glob('spec/*.md')`. Do not re-run unless `spec/` moves.

FACT: `coordination_generation` is carried by exactly fourteen request messages, and among the
seven bind-sequence RPCs only `ResumeRequest` carries it (field 14); `ShutdownRequest` carries it
at 6. So SPEC-5's new sentence "Where both appear on one message, as on `Resume` and `Shutdown`"
is exhaustive, not merely illustrative. EVIDENCE: `awk '/^message /{m=$2} /coordination_generation = /{print m}' schemas/lenny-adapter.proto`

FACT: `DemoteSDK` really does remove the registry entry, so SPEC-5's new caller-rule clause is
true against the tree. `Server.DemoteSDK` calls `anyRegisteredSession()` then `noteRuntimeClosed`
+ `releaseSessionSlot`. EVIDENCE: pkg/adapter/sdkwarm.go:296-301. Note the spec side states this
obligation for the first time in the staged §4.7.1 text; §4.7's `DemoteSDK` row (spec/04:674) and
§15.4's demotion contract (spec/15:1469) both say only "return the pod to pod-warm state".

DEFERRED [0081...non-spec-changes.md]: SCHEMA-1's field table stages NINE fields and none of them
is a request-side epoch on a bind-sequence RPC — it lists `ShutdownRequest.expected_bind_epoch`,
`ShutdownResponse.slot_reclaim`, and seven `*Response.bind_epoch` fields
(non-spec-changes.md:1033-1041). The staged spec now mandates the request side in four places:
§4.7.1 "Each of them may carry a bind epoch on its request" and "A request carrying an epoch is
admitted only when the entry ... carries that epoch" (spec-changes.md:637,639), the caller rule
"A caller echoes the epoch it holds on every later bind-sequence request" (:641), and §15.4's
non-conformance clause "An adapter that admits a request naming an epoch the entry does not carry
... does not conform" (:663). CODE-6 depends on it too: `ensureSlotStateLocked` "takes the
caller's epoch, comparing it against the entry it finds" (non-spec-changes.md:927-929) with no
stated source, and `adapterclient` gains a `BindEpoch()` latch that no bind-sequence call passes
(:964). What is true instead: the seven bind-sequence requests each need an `int64 bind_epoch`,
and standing-context #321 already banked the free numbers — `PrepareWorkspaceRequest` 5,
`FinalizeWorkspaceRequest` 6, `RunSetupRequest` 5, `AssignCredentialsRequest` 4,
`StartSessionRequest` 12, `ConfigureWorkspaceRequest` 5, `ResumeRequest` 16. The orchestrator's
already-fixed list names this defect, so it is not re-filable, but it is NOT fixed in the text.
The pass between the loops owns it.

MISTAKE: the orchestrator's "already found and fixed" list carries "The corrected mechanism
requires bind-sequence requests to carry the epoch, and SCHEMA-1 stages no request field for it"
and "CODE-6 stages no request echo and no epoch source for the seven handlers". Only the SPEC half
was fixed (the spec now states the echo rule cleanly). The schema and code halves stand exactly as
they were. A lens that trusts the fixed-list will skip the one gap that makes the whole mechanism
unimplementable.

WATCHOUT: four candidates I built and dropped, with the ground that killed each, so they are not
rebuilt at a verifier's cost.
1. "The reclaim hold is concurrency-independent but its only bound lives in a
   `maxConcurrentSessions > 1` bullet." Verified true — the `**Slot cleanup:**` bullet with
   `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` sits under
   `**Slot failure and cleanup (maxConcurrentSessions > 1).**` at spec/05:542, while SPEC-3's hold
   paragraph appends to `**Scrub model.**` at spec/05:453 ("uniform across session-mode
   configurations") and closes "The per-slot cleanup timeout the **Slot cleanup:** bullet states
   bounds the hold" (spec-changes.md:559), and §15.4 repeats it unscoped (:671). Dropped because
   the harm is nil: the hold is keyed on the slot identifier, the slot identifier IS the session
   identifier, so an unbounded hold on an exclusive pod blocks only a retry of the SAME session,
   and §6.2's pre-attached retry claims a fresh pod. Already standing as review-log Open #599.
2. "§15.4 tells a third-party adapter the per-slot cleanup timeout bounds the hold, and the
   adapter is never sent that timeout on a non-recycle `Shutdown`." Verified:
   `cleanup_timeout_seconds` lives only inside `RecycleScrub`, which is "Absent on the terminate
   path" (schemas/lenny-adapter.proto:1626-1662). Dropped as the inherited-looseness class:
   spec/05:545 already says the 5s minimum is "enforced at runtime by the adapter" and standing
   #268 records that it has no implementation, so the proposal widens rather than creates it, and
   the sibling "§15.4 names no status code" finding was refuted on the same bar.
3. "The §4.7 row calls the epoch 'a precondition on the whole request' and then scopes it to two
   of the three operations, leaving the whole-pod scrub undefined on a mismatch"
   (spec-changes.md:279 against :245). Dropped as unreachable: the recycle-disposition `Shutdown`
   is issued by `Binder.ReleaseSlot` at an ordinary session end, which holds no epoch under the
   caller rules, so an epoch-carrying recycle request cannot arise.
4. "§7.1's clause '[§5.2] keeps the retries its slot retry policy places off that pod' over-claims
   against §5.2's own text, which excludes only a pod whose reclaim DID NOT COMPLETE"
   (spec-changes.md:347 against :487). Dropped: an acknowledged reclaim has finished its tree
   removal before the gateway returns (standing #115), so the hold is down by the time the policy
   places a retry, and the two conditions coincide on every reachable ordering.

FACT: §4.7's Gateway → Adapter RPC table physically sits inside `#### 4.7.1 Role and Gateway RPC
Contract` (heading spec/04:659, tables :665-693, `#### 4.7.2` at :695). Every staged citation of
"the [Section 4.7] `Shutdown` row" therefore points one level up from where the row lives. It
resolves (the §4.7 anchor contains §4.7.1) and the repo already cites it that way, so it is not a
finding — but a lens that greps §4.7 for the row and finds the heading empty will think the
citation is false.

USEFUL [standing #515]: the `*spec-changes.md` glob really does match both `.spec-changes.md` and
`.non-spec-changes.md`. Use full filenames; I used them throughout and every line number here is
against the full name.


### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens on the spec.3 staging — BECAUSE every Kubernetes-visible surface the staged edits touch (pod-claim release, pod retire through `failed → draining → terminated`, the `leaked` occupancy hold, the `lenny.dev/drain-request` annotation) is a pre-existing mechanism this proposal reuses rather than a new write, and the amendment's two new mechanisms (bind epoch, reclaim hold) are entirely pod-local, in-memory and connection-scoped, so they touch no CRD, no status subresource, no finalizer, no admission webhook and no reconcile loop — ALTERNATIVES: I considered filing (i) the §4.1 `DeleteOptions` analogy, (ii) the Design sentence attributing the `slot_cleanup` start to gateway observation, and (iii) the edge-triggered-only reclaim with no level-triggered backstop; each is refuted below.

FACT: spec/06 pod fence carries `failed ──→ draining` and `draining ──→ terminated`, so §7.1's staged "the failed attempt releases the pod's claim and the pod retires under the §6.2 pre-attached failure disposition" is sound even though §6.2's own pre-attached bullet says only "The pod is marked `failed` and released back to the pool (or terminated if unhealthy)". Do not file that wording as a contradiction. EVIDENCE: spec/06_warm-pod-model.md:101-102, :283.

FACT: the new SPEC-5 sentence "the generation ... is validated on the RPCs that carry it" is TRUE against the published contract even though the adapter code validates `coordination_generation` only in `CoordinatorFence` and `CheckpointBarrier`. The proto comment on every carrying message states "A pod validates the generation on every gateway-to-pod RPC against the value it holds for the session the RPC names, and rejects a request whose generation does not match it (§10.1)." The code gap is pre-existing and is not this proposal's. EVIDENCE: schemas/lenny-adapter.proto:976-981, :1400-1405; pkg/adapter/coordination.go:120,:262 are the only two validation sites.

WATCHOUT: the `slot_cleanup` sub-state is a gateway-side in-memory contract model (`pkg/sandbox/slotstate`), nothing in production drives `Registry.Transition`, and §6.2's triggers are phrased in session terms rather than observer terms. The Design's new sentence at spec-changes.md:143-145 ("§6.2's `slot_cleanup` sub-state begins when the gateway observes the failure") is therefore a rationale claim the spec itself does not state, but the staged §6.2 prose it justifies states no rule at all (it points at §4.7.1 and §5.2), so the premise being loose changes no applied text. Below the bar; do not spend verifiers on it. EVIDENCE: proposals/.../0081_....spec-changes.md:143-145; spec/06_warm-pod-model.md:150-155.

WATCHOUT: the reclaim is edge-triggered inside the gateway request path with no controller-side reconcile behind it, which reads like a missing level-triggered backstop. It is not: a gateway replica that dies mid-bind leaves the per-pod `SandboxClaim` at `bound`, and §4.6.1 orphan GC drains and then DELETEs a `bound`/`recycling` claim older than `claimOrphanTimeout` whose pod no active session references. The backstop exists and the standing context already records the sibling "phantom claim / stuck finalizer" argument as killed.

USEFUL [Standing context / Traps]: the trap recording the §4.1 `DeleteOptions`-precondition analogy as verified-and-declined ("a real precondition mismatch is a 409 Conflict, while a mismatched epoch here is a SUCCESSFUL `superseded` response ... Judged below the bar because the sentence claims only the classification") is exactly the entry a Kubernetes lens walks straight into. It saved a verifier pair. Keep it.


### [spec.3.review-mechanism.4]

DECISION: returned an EMPTY findings list for the end-to-end mechanism lens on the spec staging — BECAUSE every flow I traced from origin to final effect closes, and the three candidates I built each died on evidence inside the staged text itself — ALTERNATIVES: I considered filing (a) the epoch/`coordination_generation` co-occurrence claim, (b) the "precondition on the whole request" versus the unenumerated whole-pod scrub, and (c) the DemoteSDK carve-out; all three are refuted below.

FACT: the two-mechanism fence is closed on BOTH orderings, and I re-derived it from scratch rather than trusting the narrative. Reclaim-then-successor: A's `Shutdown` at epoch 1 deregisters, the §5.2 hold starts at that critical section, B's epochless bind is refused transiently. Successor-then-reclaim: B is admitted epochless onto A's unstarted entry, the entry re-mints to epoch 2, A's `Shutdown` at epoch 1 answers `superseded` and performs nothing. Neither ordering leaves a gap. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:636 (two admission rules), :554 (`**Slot-identifier reclaim hold.**`), :242 (§4.7 row's three outcomes).

FACT: §7.1 gives a CLOSED characterisation of an incomplete reclaim — "A reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit, are the reclaims that did not complete." That is what makes `superseded` and `absent` safe with no stated `exited_cleanly` rule for them, and it is why the "§5.2 says a not-clean answer leaks the slot while §7.1 says superseded is unleaked" finding does not exist: §5.2's clause is scoped to "a cleanup on that path that does not complete", and a superseded reclaim runs no cleanup. Do not re-derive this as a contradiction. EVIDENCE: spec-changes.md:347 (§7.1 block), :551 (§5.2 append).

FACT: the epoch/generation co-occurrence sentence is now ACCURATE, and I checked it field by field. Among the seven bind-sequence RPCs only `ResumeRequest` declares `coordination_generation` (schemas/lenny-adapter.proto:1405); `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`, `RunSetupRequest`, `StartSessionRequest`, `AssignCredentialsRequest` and `ConfigureWorkspaceRequest` declare none. `ShutdownRequest` does (:1635). So §4.7.1's "Where both appear on one message, as on `Resume` and `Shutdown`, each is checked on its own terms" (spec-changes.md:636) names exactly the right two. EVIDENCE: schemas/lenny-adapter.proto:1405,:1635; the message/field census is `grep -n "^message \|^  int64 coordination_generation" schemas/lenny-adapter.proto`.

CORRECTS [Open item at review-log.md:594]: "`AssignCredentialsResponse.bind_epoch = 1` is a hard field-number collision ... the message already holds 1, 2, 3 and 5 with 4 reserved. The free number is 6." That is wrong: it describes `AssignCredentialsRequest`. `AssignCredentialsResponse` is a bare empty message, `message AssignCredentialsResponse {}` on one line, so field 1 is free and SCHEMA-1's table is right. `ShutdownRequest`'s 7 is also genuinely free (1,2,3,5,6 used, 4 reserved). EVIDENCE: schemas/lenny-adapter.proto:1033; :1609-1636.

FACT: the DemoteSDK carve-out in §4.7.1's caller rules is grounded in the tree AND in §15.4. `Server.DemoteSDK` calls `releaseSessionSlot(sessionID)` on the registry's single entry, which is `deregisterSlot` + `removeSlotTree`, synchronously inside the RPC, so the §5.2 reclaim hold that deregistration starts is released before the response returns and the pod-warm bind sequence that follows is admitted. §15.4's own demotion contract independently guarantees the post-demotion state admits materialization ("so that workspace files ... can be materialized before the agent starts"). I built and dropped a finding that the hold refuses the mandated pod-warm fallback. EVIDENCE: pkg/adapter/sdkwarm.go:296-301; pkg/adapter/slotsession.go:212-220; spec/15_external-api-surface.md:1469.

WATCHOUT: the §4.7 row calls the epoch "a precondition on the whole request" and then enumerates only two consequences (slot release, runtime teardown), leaving the recycle-disposition whole-pod scrub unnamed. It reads like a granularity mismatch and it is INERT: the recycle form is `ShutdownRecycle`, which CODE-6 sends with a zero epoch, so an epoch-carrying recycle request is unreachable. Do not spend a verifier pair on it. EVIDENCE: spec-changes.md:242; non-spec-changes.md:960-963.

WATCHOUT: §4.7.1's caller rule "A caller echoes the epoch it holds ... on a `Shutdown` for the session" sits one sentence away from "A `Shutdown` carrying no epoch is ... what every caller other than a fenced compensation sends". They look like they contradict. They do not, because the epoch latch is per-connection and only the compensation runs on the attempt's own still-open connection; every other `Shutdown` sender (session end, §11.4 revoke, the occupancy-zero recycle) is on a connection that latched nothing. Four sentences of the same paragraph have to be read together before filing on any one of them. EVIDENCE: spec-changes.md:640.

USEFUL [review-log.md:626, DEFERRED on SCHEMA-1/CODE-6 request side]: it is the reason I did not file the largest defect I found. SPEC-5 mandates that a bind-sequence request may carry an epoch and that the adapter compares it, while SCHEMA-1's nine-field table stages `ShutdownRequest.expected_bind_epoch` plus seven RESPONSE fields and no bind-sequence request field at all, so two of the three orderings in the "A start that races the reclaim" bullet and one of CONF-1's four properties currently have no wire carrier. The DEFERRED already names the remedy down to the seven free field numbers. It is a non-spec-lane edit and this loop may not author it; whoever runs the between-loops pass must land it, because the spec text this loop is certifying depends on it.

USEFUL [review-log.md:134, "The snapshot diffs have been empty for most rounds"]: `spec-r3` and `spec-r3-start` are both byte-identical to the live proposal again this round. `spec-r2` is the newest snapshot that differs, and the real r2→r3 delta is five hunks: the caller-rule rewrite ("holds the epoch it holds" + the entry-removing-RPC clause), the §6.2 hold sentence replaced by a pointer, the §7.1 epoch clause re-pointed to §4.7.1, the §4.7.1/§15.4 `Shutdown` carve-out, and the DemoteSDK sentence. Diff against `spec-r2`, not `spec-r3`.


### [spec.4.review-citations.<n>]

DECISION: returned an EMPTY findings list for the spec staging under the citation lens — BECAUSE every
concrete citation, every anchor, and every attributed behaviour in
`0081_...spec-changes.md` resolved against the tree. ALTERNATIVES: I considered filing the
§4.7.1 sentence "a request carrying one and a request carrying none ... ask for the same thing"
as self-contradictory against the same paragraph's two admission rules (epochless MINTS a fresh
epoch and takes ownership; epoch-carrying mints nothing), and dropped it: "what the request does"
reads as the operation (prepare workspace, start session) rather than the ownership bookkeeping,
so it is a framing preference rather than a false statement.

FACT: the r3→r4 delta on the spec staging is exactly two things and nothing else — the "zero is
not an epoch" clause added at four sites (spec-changes.md:105-106, :640 §4.7.1, :651 and :654 §15.4)
and the §4.1 rewrite that DELETES the epoch sentence from the staged §4.1 replacement and replaces
its rationale (:243-256, :631-635, :702). `diff -u` against `scratchpad/cp-snap/.../spec-r3` is
~140 lines; `spec-r4` and `spec-r4-start` are byte-identical to the live proposal, as the standing
trap predicts. EVIDENCE: scratchpad/cp-snap/0081_.../spec-r3 vs proposals/0081_...

FACT: the mechanical anchor sweep is still clean after the r4 rewrite. A python pass over the 31
fenced blocks of the spec staging against `spec/*.md` gives count 1 for every "text to replace"
block (0,2,5,8,10,12,14,16,18,20,22,24) and count 0 for every replacement or insertion. The §4.1
replacement changed this round and its new text still occurs zero times in spec/. Re-run this
rather than eyeballing; it takes ten lines.

FACT: the new §4.1 rationale's proto citation is exact. `schemas/lenny-adapter.proto:1630-1635` is
the `coordination_generation` comment plus `int64 coordination_generation = 6;` inside
`ShutdownRequest`, and SCHEMA-1 stages `expected_bind_epoch` as a bare `int64` (non-spec-changes.md
field table), so the rationale's "bare int64, no wire presence, exactly as the generation is" holds
on both halves. EVIDENCE: schemas/lenny-adapter.proto:1630-1635; non-spec-changes.md:1034.

FACT: `Resume` and `Shutdown` really are the only two bind-relevant messages carrying
`coordination_generation`, so §4.7.1's "Where both appear on one message, as on `Resume` and
`Shutdown`" is exact rather than illustrative. The full carrier set is SendMessage, Attach,
RotateCredentials, ExtendCredentialLease, RevokeCredentials, Interrupt, Checkpoint, SignalDeadline,
Resume, CoordinatorFence, CheckpointBarrier, ExportPaths, ReportUsage, Shutdown — none of the other
six bind-sequence requests carries one. EVIDENCE: `grep -n "coordination_generation = " schemas/lenny-adapter.proto`;
ResumeRequest field 14 at :1405.

FACT: the §4.7.1 caller rule's `DemoteSDK` clause is true on both halves. The §4.7 row states the
pod-warm return (spec/04:674) and `Server.DemoteSDK` calls `releaseSessionSlot`, which deregisters
the entry, so the post-demotion bind sequence is genuinely epochless. EVIDENCE: pkg/adapter/sdkwarm.go:296-301.

FACT: the four sites that state the reclaim hold's window now agree word for word on both endpoints
(deregistration of the registry entry → the reclaim's cleanup finishing): §5.2 append
(spec-changes.md:552), §6.2 prose (:614), §7.1 paragraph (:304-ish), §15.4 block (:662). The earlier
"third start point" finding is discharged; do not re-derive it.

USEFUL [Standing context, "The snapshot diffs have been empty for most rounds"]: saved a wasted
round. `diff -rq` over the whole `scratchpad/cp-snap/0081` directory first, then diff against the
newest snapshot that actually differs (`spec-r3` this round).

USEFUL [Standing context, "The glob `*spec-changes.md` matches BOTH files"]: real. Use the full
filename on every `sed`/`grep`.


### [spec.5.fix-G1.1]

DECISION: SPEC-5's admission rules are now evaluated once per RPC CALL, and `PrepareWorkspace`'s streaming form is stated explicitly in both staged blocks: every frame of one call carries the same bind epoch, admission is evaluated once at the frame from which the adapter first resolves the slot identifier, later frames are neither admitted nor refused on their own, and the single response reports the epoch that admission fixed. §15.4 gains the matching non-conformance case ("evaluates the admission rules per `PrepareWorkspace` frame, or mints more than one epoch for one call"). BECAUSE the shipped adapter already resolves the entry exactly once from the first frame's session id, and repeating the epoch on every frame mirrors `session_id`, which every frame already repeats. ALTERNATIVES: put the epoch on the first frame only (rejected: under "zero carries none" every later frame then reads as an epochless request against a not-started entry, which rule 1 obliges the adapter to re-mint on, so it recreates the defect); drop `PrepareWorkspace` from the bind-sequence set (rejected: an upload-bearing attempt would then hold no epoch until `FinalizeWorkspace`); state it only in CODE-6 (rejected: §15.4 is the published third-party contract).

DECISION: the §7.4 mid-session upload is kept INSIDE the bind-sequence set with no carve-out. The caller echo rule was re-scoped from "every later bind-sequence request of that attempt" to "every later bind-sequence request it sends for that session on the connection the epoch was reported on", and both blocks now state that a request echoing the entry's epoch is admitted whether or not that entry's session has started. §15.4 gains a fourth non-conformance arm for refusing an echoing request because the session has started. BECAUSE the mid-session upload runs on the successful bind's own adapter client, so the per-connection latch already holds the right epoch and rule 2 admits it. ALTERNATIVES: the finding's own carve-out exempting the mid-session pair (rejected: `PrepareWorkspaceRequest` carries no mid-session marker at all, so a third-party adapter cannot classify the stream without a new proto field, and the carve-out would force a CODE-6 `ensureSlotPaths` exception and a CONF-1 exception); relax rule 1 to admit an epochless request onto a started entry for mid-session uploads (rejected: that removes the bar the epoch exists for); give the mid-session upload its own connection and handshake (rejected: `upload_to_session.go` answers `TARGET_NOT_READY` when the replica holds no live binding, so the binding's client is the only carrier).

FACT: the §7.4 mid-session upload reuses the BIND's own adapter connection. `BindResult.Adapter` is the client the slot bind dialled (pkg/gateway/podlifecycle/podsession/slotbinder.go:332, field declared at binder.go:521-523 with "the caller owns it and closes it when the session ends"), and pkg/gateway/sessionserver/upload_to_session.go:128,134 calls `bind.Adapter.PrepareWorkspace` then `bind.Adapter.FinalizeWorkspace(..., midSession=true)` on it. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:117-135.

FACT: `PrepareWorkspace` is the only client-streaming bind-sequence RPC and its request message carries no mid-session marker; only `FinalizeWorkspaceRequest.mid_session` (field 4) exists. EVIDENCE: schemas/lenny-adapter.proto:41 (`rpc PrepareWorkspace(stream PrepareWorkspaceRequest) returns (PrepareWorkspaceResponse)`), :682-696, :724.

FACT: the shipped adapter resolves the staging directory once, from the first frame's session id, under an `if stagingDir == ""` guard, and checks later frames only for a non-empty session id. EVIDENCE: pkg/adapter/staging.go:44-47, :66-83.

WATCHOUT: the anchor `07_session-lifecycle.md#74-upload-safety` is already used from spec/13, spec/18 and spec/08, so the two new citations mint nothing. Do NOT add a §7.4 row to "Spec sections deliberately untouched": a citation is not an edit and the fix imposes no obligation on §7.4. EVIDENCE: spec/07_session-lifecycle.md:438; spec/13_security-model.md:741.

WATCHOUT: in the §4.7.1 admission paragraph the sufficiency clause must sit inside the admit sentence rather than after the refusal clause. A first draft wrote "…the adapter refuses the request as a transient condition and creates nothing. It is admitted whether or not…", where "It" reads as the refused request. Both blocks now read "admitted exactly when the entry … carries that epoch, whether or not that entry's session has started".

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md, CONF-1's echo property at the bullet beginning "A bind-sequence request echoing the identifier's current epoch is admitted and does not re-mint"]: the property is now incomplete rather than false. It should read that such a request is admitted, whether or not the entry's session has started, and does not re-mint, and the battery wants one case driving a §7.4 `FinalizeWorkspace` with `mid_session` set onto a started session over the bind connection, and one case asserting that every frame of a multi-frame `PrepareWorkspace` call carries the same non-zero epoch and the call is admitted exactly once.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md, the CODE-6 latch rationale and the verification-set paragraph]: the rationale sentence "the connection ... is dialled once per bind attempt and closed with that attempt" is loose now that the spec requires the epoch to be echoed on the session's later requests over that same connection: on a SUCCESSFUL bind the connection becomes `BindResult.Adapter` and is closed when the SESSION ends (pkg/gateway/podlifecycle/podsession/binder.go:521-523), which is what carries the §7.4 mid-session upload. The verification-set paragraph's "the client echoes what the latch holds on the attempt's later bind-sequence requests" should say the later bind-sequence requests that connection carries for the session. Both are true of the latch as staged and need only the wording.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md, the deferred request-side wiring recorded at review-log.md:626]: that wiring must stamp the latched epoch on the mid-session `PrepareWorkspace` and `FinalizeWorkspace` calls too, which it gets for free because `Client.PrepareWorkspace` and `Client.FinalizeWorkspace` are the single shared methods and the latch is per-connection, and it must stamp the same value on EVERY frame `sendUpload` emits rather than on the first.

USEFUL [spec.1.fix-bind-epoch, and the Settled entry "the epoch is minted per admitted bind ATTEMPT"]: the per-attempt framing is what made the echo-scoping fix legible; a per-entry epoch would have made the mid-session upload's admission a different question.


### [spec.5.fix-G2.1]
DECISION: Replaced the ground for "a superseded reclaim is completed and unleaked" at all four staged sites with the ownership ground, keeping every conclusion (completed reclaim, unleaked, CODE-4's `return false`) unchanged — BECAUSE the withdrawn ground ("the state the failed attempt created is already gone") is false on the primary superseded ordering: SPEC-5 rule 1 admits an epochless successor onto a surviving unstarted entry, and the successor inherits that entry and its on-disk tree. ALTERNATIVES: (a) delete the ground clause and point at SPEC-5's outcome definitions, rejected because the §4.7 row and the §7.1 leak disposition each need their own one-clause reason and the corrected clause is one clause; (b) narrow SPEC-5 rule 1 so the successor is refused, rejected because that deletes the create-time-reserved retry's only recovery path.
FACT: `ensureSlotStateLocked` returns the existing `*slotState` when the map already holds the identifier, and `slotlayout.EnsureTree` runs only on the create branch, so an admitted epochless successor inherits the predecessor's `paths`, credential map and on-disk slot tree rather than a fresh one — EVIDENCE: pkg/adapter/slot.go:105-124.
WATCHOUT: the invalid inference reappears in a fifth place the design adjudicated as needing no edit. SPEC-5's §4.7.1 admission paragraph ends "A refused request mints nothing, so an attempt that has been superseded can hold no state under the identifier." The premise is about the refused request and the conclusion is about all state the attempt ever created, which is the same step this round withdrew elsewhere. It is true read as "owns no state" and false read as "created no state" — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:648.
FACT: the ownership wording this round installed is the wording SPEC-5's outcome definitions already use ("a later bind attempt owns the slot identifier and the reclaim removed nothing"), so the four corrected sites and the outcome definitions now speak in one voice — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:676; the same sentence appears in the staged proto enum comment at ...non-spec-changes.md:1023.
OPEN: whether the §4.7.1 sentence at spec-changes.md:648 should be re-grounded the same way, or whether "hold" there is already read as "own". A later round should either reword it to "owns no state under the identifier" or record that the current reading is intended. Editing it was out of this group's adjudicated scope.
USEFUL [spec.5.fix-G1]: G1's hand-off named exactly which sentences it had rewritten in the SPEC-5 blocks, which is how I knew the admission paragraph had drifted from :644 to :648 and could anchor on quoted strings rather than line numbers.


### [spec.5.fix-design-G1.1]

DECISION: close BOTH G1 findings by TIGHTENING the two SPEC-5 blocks in place — one sentence fixing admission granularity to the RPC CALL (with every `PrepareWorkspace` frame carrying the SAME epoch), and one sentence re-scoping the caller rule from "that attempt" to "that session, on the connection the epoch was observed on" so the §7.4 mid-session `PrepareWorkspace`/`FinalizeWorkspace` pair echoes the live binding's epoch and is admitted under rule 2 — BECAUSE the §7.4 upload runs over `BindResult.Adapter`, which IS the successful bind attempt's own `*adapterclient.Client` (pkg/gateway/podlifecycle/podsession/slotbinder.go:332, binder.go:523; pkg/gateway/sessionserver/upload_to_session.go:111-128), so CODE-6's per-connection epoch latch already holds the right value there and the mid-session request is never epochless in any reachable case. ALTERNATIVES rejected: (a) carve the §7.4 mid-session `PrepareWorkspace`/`FinalizeWorkspace` out of the bind-sequence set, as finding 2 suggested — rejected because `PrepareWorkspaceRequest` carries NO mid-session marker (schemas/lenny-adapter.proto:682-696; only `FinalizeWorkspaceRequest.mid_session` field 4 exists at :724), so a conforming adapter cannot distinguish the two callers even in principle without a NEW proto field, and the carve-out would additionally force a CODE-6 `ensureSlotPaths` carve-out and a CONF-1 exception clause — a second mechanism plus an exception clause to avoid one re-scoped sentence, i.e. hair; (b) epoch on the FIRST `PrepareWorkspace` frame only — rejected because "zero carries none" (spec-changes.md:640) then makes every later frame read as an epochless request, which is the exact trap finding 0 names; repeating the epoch on every frame mirrors `session_id`, which is already repeated on every frame; (c) deleting `PrepareWorkspace` from the bind-sequence set — rejected because it is the first entry-touching RPC when the plan carries uploads and SCHEMA-1 makes its response the required epoch report (non-spec-changes.md:1035).

FACT: the §7.4 mid-session upload reuses the BIND ATTEMPT'S OWN adapter connection, not a fresh dial. `slotbinder.go:332` stores the attempt's `cl` into `BindResult.Adapter`, the session registry hands it back, and `upload_to_session.go:128,134` calls `bind.Adapter.PrepareWorkspace` / `.FinalizeWorkspace(..., midSession=true)` on it. This is what makes the echo design work with zero new wire surface. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:332; pkg/gateway/podlifecycle/podsession/binder.go:523; pkg/gateway/sessionserver/upload_to_session.go:111-135.

FACT: `Client.PrepareWorkspace` sends one frame per 64 KiB per upload and the adapter resolves the staging dir ONCE, from the first frame's session id, then checks later frames only for a non-empty session id. Per-call admission is therefore what the shipped adapter already does; the spec text is what is behind. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:240,247-285; pkg/adapter/staging.go:44-47,66-83.

WATCHOUT: rule 2 at spec-changes.md:644/:668 says a request carrying an epoch "is admitted ONLY when" the entry carries it. That is a necessary condition with no sufficiency statement, and a reader who carries rule 1's started-session bar across into rule 2 concludes the mid-session upload is refused. The fix must state explicitly that an echoing request is admitted whether or not the entry's session has started, and that rule 1's started-session bar exists to stop a NEW attempt taking an identifier a started session owns. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:644,:668.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, CONF-1 at :985 and the tier-3 list at :1365-1377]: after this fix the CONF-1 property "one carrying no epoch is refused when the entry's session has started" stays TRUE and needs no exception, but it is now incomplete in one respect the spec lane cannot land: the echo arm should read "admitted, whether or not the entry's session has started, and does not re-mint", and the battery wants one case driving a §7.4 mid-session `FinalizeWorkspace(mid_session=true)` onto a started session over the bind connection and asserting it is admitted. Same lane owes a tier-3 case asserting every frame of one `PrepareWorkspace` call carries the same non-zero epoch and the call is admitted once.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, SCHEMA-1/CODE-6 request-side wiring, the standing entry at review-log.md:626]: that entry stages `PrepareWorkspaceRequest bind_epoch = 5`. This round fixes its semantics: the field is set to the SAME value on every frame of one call by `sendUpload` (pkg/gateway/runtime/adapterclient/client.go:268-285), and the adapter evaluates admission once, in `resolvePrepareStagingDir`'s `if stagingDir == ""` arm (pkg/adapter/staging.go:75-83), ignoring the field on later frames of the same call. Whoever closes 626 must carry that, or the wiring re-mints per chunk.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:934]: `ensureSlotPaths` "widens to return the epoch beside the paths, because its callers are the workspace RPCs that must report one" stays TRUE under the echo design (the mid-session calls do report an epoch), so no carve-out is owed. Recorded explicitly so a later round does not re-file the mid-session carve-out the finding proposed.

UNVERIFIED: whether a coordinator handoff can leave `bind.Adapter` pointing at a connection whose epoch latch was never set. The §7.3 re-attach goes through `Binder.Resume` and `ResumeResponse` carries `bind_epoch` (non-spec-changes.md:1037), and `upload_to_session.go` answers `TARGET_NOT_READY` when no binding exists on the replica, so no reachable epochless case was found; a non-spec-lane reviewer should confirm against `binder.go:1643`'s `BindResult` construction.


### [spec.5.fix-design-G2.1]

DECISION: Correct the GROUND, keep the conclusion, at four sites (spec-changes.md Design ~:123-125, §4.7 `Shutdown` row :283, §7.1 paragraph :351, and the CODE-4 comment non-spec-changes.md:558-560). The true ground is OWNERSHIP, not absence: on `superseded` a later attempt owns the slot identifier and whatever was created under it, and the failed attempt's own bind requests are refused, so it owns nothing the reclaim could release. On `absent` the adapter holds nothing for the session. BECAUSE the "already gone" ground is false on the primary superseded ordering and the staged SPEC-5 admission rule (spec-changes.md:644) is what makes it false. ALTERNATIVES: (a) delete the ground clause at §4.7/§7.1 and cite SPEC-5's outcome definitions instead — rejected, each site is the home of a different obligation (the RPC row and the gateway-side reclaim duty) and the corrected clause is one clause, so a pointer buys nothing; (b) narrow SPEC-5 rule 1 so an epochless successor cannot adopt a surviving entry — rejected, that deletes the create-time-reserved recovery path (:172-175) to avoid correcting a sentence.

FACT: SPEC-5 already states the correct ground twice and neither is a site. spec-changes.md:644 "A refused request mints nothing, so an attempt that has been superseded can hold no state under the identifier" reads as ownership and is true; :672 "a later bind attempt owns the slot identifier and the reclaim removed nothing" is the model wording to copy. Same for spec-changes.md:215, non-spec-changes.md:986, :1019, :1023 — all say "removed nothing"/"creates nothing" about the refused request, all true. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:644,672

FACT: the successor really does inherit. `ensureSlotStateLocked` returns the existing `*slotState` unchanged when the map already holds the id, and `slotlayout.EnsureTree` runs only on the create branch, so the predecessor's resolved paths, credential map and on-disk tree survive into the successor's session. EVIDENCE: pkg/adapter/slot.go:105-121

WATCHOUT: do not pull spec/05:556's "Fresh workspace guarantee" into this edit. Four lenses in the earlier converged run already adjudicated that pairing: the guarantee is scoped to the §5.2 `maxConcurrentSessions > 1` slot retry policy, which always assigns a NEW slot, while the inheritance here arises only on the create-time-reserved path that policy never places. Correcting the ground does not newly falsify it. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:542,553,556

WATCHOUT: the CODE-4 comment at non-spec-changes.md:558-560 is IN SCOPE, not a separate finding — it repeats the exact sentence being withdrawn, so leaving it makes the staged code comment contradict the corrected spec. Its `return false` conclusion is right and must stand.


### [spec.5.review-applicability.1]

DECISION: returned an EMPTY findings list — BECAUSE the whole spec staging was re-verified mechanically as an executable procedure and every anchor, replacement, link and gate held — ALTERNATIVES: filing the §4.1-rationale "no wire presence" claim (rejected: it is unapplied rationale, the applied §4.1 sentence says nothing about the epoch, so no applied inconsistency, and it is a mechanism/citation call with high refutation risk under the default-to-refuted rule); filing the §7.1 "spends one of the attempts the retry policy allows" clause against the same sentence's "§5.2 keeps the retries its slot retry policy places off that pod" (rejected: reads coherently as consequence-then-mitigation, and it is mechanism rather than applicability); re-filing the S1-cites-§4.7.1-content-S5-lands forward reference (rejected: recorded at Standing context Open as deliberately not filed, remedy is a checklist reorder which this loop's scope bars, and the orchestrator excludes checklist drift).

FACT: the anchor sweep is re-runnable in one command and came back clean on the CURRENT text, including the two blocks this round rewrote. Twelve anchor blocks resolve to exactly one site each in `spec/*.md`; every replacement/insertion block resolves to zero. Script: extract every ```-fenced block after `## Staged edits` and `str.count` it across `spec/*.md`. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:240,276,331,375,398,410,439,458,485,514,534,555 (count 1 each); :246,283,302,337,351,381,404,416,445,464,491,520,540,561,603,621,640,664,676 (count 0 each).

FACT: every markdown anchor the staged text mints resolves against a real heading, and every bare (current-file) anchor lands in the file its own block targets. Checked by slugging every `^#{1,6}` heading in `spec/*.md`: `#47-runtime-adapter`, `#471-role-and-gateway-rpc-contract`, `#71-normal-flow`, `#73-retry-and-resume`, plus the nine cross-file forms. — EVIDENCE: spec/04_system-components.md:657,659,848,1099; spec/07_session-lifecycle.md:3,378; spec/15_external-api-surface.md:614,1686; spec/06_warm-pod-model.md:78; spec/05_runtime-registry-and-pool-model.md:365; spec/10_gateway-internals.md:3.

FACT: all six prose-described insertion points resolve uniquely in the tree today. §4.1 paragraph (spec/04:157), §7.1 atomicity paragraph end + the continuation line (spec/07:23,:24), §6.2 fence heading + the `receiving_uploads ──→ running` entry (spec/06:150,:152-153), the `**`reserved` hold semantics.**` paragraph that follows the fence (spec/06:158), `*Adapter → Gateway RPCs:*` table close before `#### 4.7.2` (spec/04:688,:695), `**SDK-warm demotion contract:**` before `#### 15.4.1` (spec/15:1469,:1471), §7.3's numbered-list tail (spec/07:402ff), §5.2's `**Slot retry policy` bullet (spec/05:553), §29.4 step 13 (spec/29:704-711).

FACT: the §4.7 `Shutdown` row's tier-11 gates survive SPEC-1's replacement, re-verified against the CURRENT row text rather than trusting the standing entry. `recycle_scrub_trigger_consistency_test.go` needs `recycle disposition`, `ReportPodScrub`, `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`, `does not block the response on the scrub`, and the literal `05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes` — every one of them sits in the row remainder SPEC-1 leaves from "On the default disposition the pod is replaced." onward. Its `s47` is `specSection(spec/04, "### 4.7 ")`, so SPEC-5's §4.7.1 block is INSIDE that slice; the block carries no `| ` + backtick-Shutdown table row, so `requireLine(s47, "| `Shutdown` |")` still resolves to the real row. — EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:57,65,74-100,138-142; spec/04_system-components.md:686.

FACT: the §5.2 append's first-match hazard is still inert against the CURRENT append text. The four gates anchored in `s52` key on `Whole-pod replacement trigger` (capital W), `Session count limit`, `Uptime limit`, `The gateway triggers the whole-pod scrub`, `**Slot (session mode).**` and `A service-mode slot is a different thing`; the appended text writes only the lower-case `whole-pod replacement trigger stated below`, so `lineContaining`'s case-sensitive first-match scan cannot be redirected. The three `s62` anchors (`` `leaked` slot semantics ``, `served-session count reaches recycle.maxSessionsPerPod`, `level-triggers the drain from the pod CreationTimestamp`) are likewise absent from SPEC-4's new prose paragraph. — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:82,92,155,165,298,314; tests/tier11_docs/slot_definition_glossary_reconciliation_test.go:84,127; proposals/0081_.../0081_....spec-changes.md:561,621.

FACT: SPEC-2's §7.3 append cannot break `external_side_effect_recovery_test.go`. That file's §7.3 assertions are a `requireAllContain` presence set plus a `requireNoneContain` forbidden set (`at-most-once`, `exactly-once tool-call`, `exactly-once semantics`, `exactly-once guarantee`); the appended sentence carries none of the forbidden strings and removes none of the required ones. — EVIDENCE: tests/tier11_docs/external_side_effect_recovery_test.go:101-116,:175-195; proposals/0081_.../0081_....spec-changes.md:445.

FACT: `ShutdownRequest.coordination_generation` really is a bare `int64` at field 6, so the §4.1 rationale's parenthetical citation `schemas/lenny-adapter.proto:1630-1635` covers the comment plus the field and is accurate as a range. SCHEMA-1 stages `expected_bind_epoch` as a bare `int64` too, so the rationale's "as the `coordination_generation` fence the same message already carries is" is consistent with what the non-spec lane stages. — EVIDENCE: schemas/lenny-adapter.proto:1629-1635; proposals/0081_.../0081_....non-spec-changes.md:1033.

WATCHOUT: the `## Spec files touched` list is now complete against the staged blocks — six files, thirteen edit sites, and each staged block has a matching entry. A fixer that adds or drops a staged block must sweep that list; it is the only place the edit inventory is stated as a set. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:700-719.

WATCHOUT: the §5.2 append and the §15.4 insert each stage a fenced block that contains a BLANK LINE, so each lands as two markdown paragraphs even though the instruction says "Append to that paragraph". This is intended (the second paragraph is `**Slot-identifier reclaim hold.**`) and `## Spec files touched` names it, but an implementor reading "append to that paragraph" literally would splice the reclaim-hold text into the scrub-model paragraph. Do not "fix" the instruction by removing the blank line. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:558-564,:663-677,:705-707.

USEFUL [Standing context, Settled "The staged-anchor sweep now has a mechanical, repeatable form"]: re-running the ten-line script cost about a minute and covered the two blocks this round rewrote, which is exactly the case the entry says it is for. Keep the entry.
USEFUL [Standing context, Traps "The glob `*spec-changes.md` matches BOTH files"]: saved a whole wasted read; I used full filenames throughout.


### [spec.5.review-citations.5]

DECISION: returned an empty findings list — BECAUSE every concrete citation in `spec-changes.md` verified against the tree this round, including the newest SPEC-5 text — ALTERNATIVES: filing the DEFERRED pronoun defect at review-log.md:93 (edge-case bullet ":109-111", "when that pod is the pool's only candidate") and the "as on `Resume` and `Shutdown`" example clause; both rejected, the first because it is already a recorded DEFERRED in unapplied prose, the second because it turns out to be exactly right (see FACT below).

FACT: the anchor sweep is now mechanically re-verifiable in one command and it is CLEAN for all thirteen fenced "text to replace" blocks: each occurs exactly once across `spec/*.md`, and every "replace it with" block occurs zero times. Script: extract ```-fenced blocks from spec-changes.md with `re.findall(r'```\n(.*?)\n```', txt, re.S)` and `c.count(b)` over `glob.glob('spec/*.md')`. Do not re-run by hand. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:239-247,275-284,330-338,374-382,397-405,409-417,438-440,457-465,484-492,513-521,533-541,554-556

FACT: among the seven bind-sequence requests, ONLY `ResumeRequest` carries `coordination_generation`; `Shutdown` is the only other message where both fields will co-occur. So §4.7.1's "Where both appear on one message, as on `Resume` and `Shutdown`" is not a loose example, it is the exhaustive pair. `AssignCredentialsRequest`, `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`, `RunSetupRequest`, `StartSessionRequest` and `ConfigureWorkspaceRequest` carry no generation. Derive it with `awk '/^message /{m=$2} /coordination_generation *= *[0-9]+;/{print NR": "m}'`. — EVIDENCE: schemas/lenny-adapter.proto:1405 (ResumeRequest), :1635 (ShutdownRequest)

FACT: `DemoteSDK` really does remove the slot registry entry, so §4.7.1's demotion clause is grounded even though the §4.7 `DemoteSDK` row says only "return the pod to pod-warm state". The handler calls `anyRegisteredSession()` then `noteRuntimeClosed` + `releaseSessionSlot`. — EVIDENCE: pkg/adapter/sdkwarm.go:294-298

FACT: `schemas/lenny-adapter.proto:1630-1635` is EXACT for `ShutdownRequest.coordination_generation` (comment :1630-1634, field :1635), so the SPEC-1 §4.1 rationale's one proto citation needs no normalisation. — EVIDENCE: schemas/lenny-adapter.proto:1635

FACT: the §15.4 insertion point is unambiguous and untouched — `**SDK-warm demotion contract:**` at spec/15_external-api-surface.md:1469, `#### 15.4.1 Message Format and Binary I/O Requirements` at :1471, nothing between them. Same for §4.7.1: the `*Adapter → Gateway RPCs:*` table ends at spec/04_system-components.md:693 and `#### 4.7.2` opens at :695. Both new SPEC-5 blocks land in a two-line gap. — EVIDENCE: spec/15_external-api-surface.md:1469,1471; spec/04_system-components.md:693,695

FACT: `#471-role-and-gateway-rpc-contract` resolves (heading `#### 4.7.1 Role and Gateway RPC Contract`, spec/04_system-components.md:659). It is a newly minted anchor this proposal introduces and it was NOT in the Settled anchor list; it is now checked. — EVIDENCE: spec/04_system-components.md:659

FACT: `Shutdown` appears nowhere in `spec/28_communication-channels.md` (grep returns nothing), and §28.5.1's cards carry a per-channel `**Messages.**` bullet rather than a field list, so the "deliberately untouched · §28's registers" row is exact. — EVIDENCE: spec/28_communication-channels.md:205,231-236

FACT: §29.4's three cited line spans are all accurate as written: the `**Preconditions.**` paragraph at :586-588 ("so the runtime is running"), the interrupt-path addition at :589-591, step 10's terminate clause at :669-674, and step 12's "the adapter closes the session runtime" at :697. — EVIDENCE: spec/29_communication-scenarios.md:586-591,669-674,697

USEFUL [Traps · "Citation ranges to normalise, not to file"]: saved a verifier pair. The three remaining line-number citations in spec-changes.md (`spec/04:151`, `spec/04:157`, `spec/29:586-588`) are all exact, so that trap now has nothing left to normalise inside the SPEC lane's file.

WATCHOUT: `diff -rq` between the round-5 snapshot at scratchpad/cp-snap/.../spec-r5 and the live proposal directory returns NOTHING — the proposal is byte-identical to the snapshot. A reviewer who plans their reading around "read the changed sections hardest" gets no signal from the diff this round and must sweep the whole document.


### [spec.5.review-client-surface.1]

FACT: `PrepareWorkspace` and `FinalizeWorkspace` have a SECOND, non-bind production caller: the §7.4
mid-session upload handler, which calls both on a session that is already running, over the
session's live pod binding. Both are named in SPEC-5's "bind-sequence RPCs" set, whose admission
rules refuse an epochless request onto an identifier whose session has started. Every lens that
reasons about the seven bind-sequence RPCs must check this caller.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:128,:134; schemas/lenny-adapter.proto:724
(`bool mid_session = 4;`); spec/07_session-lifecycle.md:444.

FACT: `PrepareWorkspace` is CLIENT-STREAMING (`rpc PrepareWorkspace(stream PrepareWorkspaceRequest)
returns (PrepareWorkspaceResponse)`), and the shipped client sends one `PrepareWorkspaceRequest`
per 64 KiB frame per upload, each carrying `session_id`. So "each bind-sequence request may carry a
bind epoch ... and reports the epoch on the response" is a 1:1 statement over a N:1 RPC.
EVIDENCE: schemas/lenny-adapter.proto:41; pkg/gateway/runtime/adapterclient/client.go:247-264,:268-285.

FACT (verified, do not re-derive): `ResumeRequest` (`coordination_generation = 14`) and
`ShutdownRequest` (`= 6`) are the ONLY bind-sequence requests carrying a coordination generation, so
SPEC-5's "as on `Resume` and `Shutdown`" is exact. `#471-role-and-gateway-rpc-contract`,
`#101-horizontal-scaling` and the §15.4 insertion anchor (`**SDK-warm demotion contract:**`,
spec/15:1469, immediately before `#### 15.4.1` at :1471) all resolve. §15.4.6 really is a RUNTIME
conformance suite run against a fake adapter, so the "deliberately untouched" justification holds.
`Shutdown` appears in no §28.5.1 register row (that boundary carries CH-ATTACH, CH-CHECKPOINT,
CH-FENCE, CH-BARRIER, CH-PODHEALTH only), so the §28 "untouched" claim holds.
EVIDENCE: schemas/lenny-adapter.proto:1082,1635; spec/15_external-api-surface.md:1469,:1471,:2038;
spec/28_communication-channels.md:205-220.

FACT: `DemoteSDK` does remove the registry entry, so SPEC-5's caller rule for the post-demotion
epochless bind sequence is grounded: `Server.DemoteSDK` calls `noteRuntimeClosed` +
`releaseSessionSlot`, and `releaseSessionSlot` deregisters and calls `removeSlotTree`
SYNCHRONOUSLY, so the §5.2 reclaim hold it starts also ends inside the same RPC and cannot wedge
the pod-warm fallback.
EVIDENCE: pkg/adapter/sdkwarm.go:274-302; pkg/adapter/slotsession.go:214-220.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, SCHEMA-1]: the table at :1041 still stages
`AssignCredentialsResponse.bind_epoch = 1`, and the prose at :1043 still says "`AssignCredentialsResponse`
is an empty message today, so its first field is 1". It is not empty: `session_id = 1`, `leases = 2`,
`rotation_trigger = 3`, `coordination_generation = 5`, `4` reserved. `bind_epoch = 1` is a hard
collision `buf` rejects; the free number is 6. This is already the standing Trap at review-log.md
`### Traps` ("MISTAKE: SCHEMA-1 asserts ..."), and it is STILL UNFIXED in the live staging.
EVIDENCE: schemas/lenny-adapter.proto `message AssignCredentialsResponse`;
0081_....non-spec-changes.md:1041,:1043.

USEFUL [Settled, "No per-slot sub-state is client-visible"] and [Settled, "The per-slot vocabulary has
no SDK, CRD or OpenAPI mirror ... the OpenAPI document is at pkg/gateway/externalapi/openapi/openapi.json"]:
both saved a full sweep of the REST/OpenAPI/SDK/CRD parallels, which this staging genuinely does not
reach.


### [spec.5.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE every docs-alignment defect I could
substantiate against the staged spec edits either (a) has its remedy in `docs/` or
`non-spec-changes.md`, which this loop may not close, or (b) is already recorded as a
DEFERRED in the review log. ALTERNATIVES: I considered filing the accepted failure mode
"a reclaim the adapter never answers leaves the identifier refusing every further attempt
until the pod terminates" (spec-changes.md:189-191) as an outcome that lands in no staged
spec sentence; I did not, because §7.1 and §5.2 both state the `leaked`/pod-retirement
disposition that bounds it on both pod classes, and every completeness-shaped finding in
this loop's refuted list died on exactly that reasoning.

FACT: the r5 snapshot is byte-identical to the live proposal and so is `spec-r5-start`;
the last staging delta is `spec-r3` → live and it is exactly two things: the §4.1 rework
(the epoch is now a bare scalar precondition, so §4.1 states nothing about it) and the
zero-is-not-an-epoch sentences added to §4.7.1 and §15.4. `spec-r4` differs only in the
review log. EVIDENCE: diff -rq over scratchpad/cp-snap/0081_.../spec-r4 vs the proposal
returns only the review-log file.

FACT: the §4.1 rework's load-bearing premise checks out. `coordination_generation` is a
bare `int64` at schemas/lenny-adapter.proto:1635 inside the 1630-1635 comment block the
proposal cites, and SCHEMA-1 stages `expected_bind_epoch` as a bare `int64` too
(non-spec-changes.md:1033). So "a field with no wire presence cannot engage §4.1's
presence rule" is consistent across the spec lane and the schema lane. Do not re-derive.

FACT: `DemoteSDK` really does remove the adapter's slot registry entry, so §4.7.1's caller
rule is true against the tree even though the §4.7 row it cites says only "return the pod
to pod-warm state". EVIDENCE: pkg/adapter/sdkwarm.go:274-303, the
`s.releaseSessionSlot(sessionID)` at :297; spec/04_system-components.md:674.

FACT: §4.7.1's insertion point resolves. §4.7.1 is the heading at
spec/04_system-components.md:659, `*Adapter → Gateway RPCs:*` is at :688 and its table ends
at :693, and `#### 4.7.2` is at :695. §15.4's insertion point resolves too: the
`**SDK-warm demotion contract:**` paragraph is spec/15_external-api-surface.md:1469 and
`#### 15.4.1` is :1471.

FACT: `Shutdown` appears in no §28 register row (`grep Shutdown spec/28_...md` returns
nothing), so the "§28's registers untouched" line in "Spec sections deliberately untouched"
is sound. §15.4.2 (spec/15:1686-1704) is an adapter-PROCESS machine with no per-session RPC
rows, so the new `Shutdown` outcomes reach it neither.

DEFERRED [docs/reference/adapter-contract.md]: after SPEC-5 lands, §15.4 publishes two new
mandatory third-party adapter obligations (the bind-epoch contract and the slot-identifier
reclaim hold, spec-changes.md:664-676). `docs/reference/adapter-contract.md` is the
reader-facing mirror of that published contract and carries neither, and its `DemoteSDK`
row at :64 ("Tear down the pre-connected SDK process and return the pod to pod-warm state")
is now incomplete against §4.7.1's caller rule, which makes the demotion the act that drops
the entry and forces the next bind sequence epochless. This is a widening of the standing
DEFERRED on that file (review-log.md:613, :869), not a new one; a second DOCS deliverable
against `adapter-contract.md` should cover :64 alongside :75.

USEFUL [standing context, "The snapshot diffs have been empty for most rounds"]: saved me a
whole round of hunting a fix-stage delta that does not exist. The `diff -rq` over the whole
cp-snap directory first is the right move and it is worth keeping at the top of that entry.

USEFUL [standing context, the `docs/reference/adapter-contract.md:75` dead-end resolution
and the "four sites that stay TRUE" DEFERRED]: between them they fenced off the whole
`docs/` sweep for this lens in one read, so I spent my budget on the staged spec text
instead of re-greping five files eleven other lenses have already greped.


### [spec.5.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site lens on the spec staging — BECAUSE every spec/ surface I could reach that the staged identifiers or retired concepts touch is either already in the edit list, already adjudicated in the log, or lands in docs//schemas/ and therefore in the non-spec loop — ALTERNATIVES: I considered filing (a) spec/29 §29.2 step 10's rollback restatement, (b) §6.2's `slot_cleanup ──→ released` fence annotation as a stale parallel action list against SPEC-3's widened §5.2 list, (c) spec/16 + docs/reference/metrics.md having no `lenny_adapter_leaked_slots` row; each is refuted or out of scope, see below.

FACT: the snapshot at scratchpad/cp-snap/.../spec-r5 was byte-identical to the live proposal directory this round (`diff -rq` empty), so the "read what changed first" instruction had nothing to point at. Do not spend a tool call on the diff before checking it is non-empty.

FACT: spec/29's own preamble subordinates a trace to the section it cites ONLY on disagreement, and §29.2's atomicity restatement (spec/29_communication-scenarios.md:199-201) covers its own steps 2-10, which end at the `session_id` return. The first pod-side RPC in that trace is later, so SPEC-2's widened §7.1 parenthetical ("releases the pod claim, and reclaims the state the attempt created on the pod") quantifies over an empty set there and spec/29:200 stays accurate unedited. Same reasoning kills spec/15_external-api-surface.md:594 ("both roll back with no leaked pod or credential lease"), whose "both" is the §7.1 step-3 pre-check and the step-4 claim, both pre-RPC. EVIDENCE: spec/29_communication-scenarios.md:23-25,:199-201; spec/15_external-api-surface.md:593-594. This duplicates review-log.md:108; I re-derived it from the tree and it holds.

FACT: §6.2's pre-attached failure disposition really does retire the pod, despite spec/06_warm-pod-model.md:283 reading "The pod is marked `failed` and released back to the pool". The pod-level fence carries `failed ──→ draining` and `draining ──→ terminated` (spec/06_warm-pod-model.md:101-102), so the proposal's repeated "the pod retires ... so the residue dies with the pod" (spec-changes.md:54-57, :204-208, :561) is sound. Do not file the :283 wording as a contradiction.

FACT: the CH-RUNTIMEOPS `terminate` frame genuinely carries no session field — its message-schema row is `type`, `deadlineMs`, `reason` (spec/28_communication-channels.md:1082) — and §28.5.3 states the frame's timing and degradation but never its trigger (spec/28:1100-1130). So SPEC-1's §29.4 step-13 append ("The frame is pod-global and names no session") is accurate and §28 needs no companion edit, which is what the "Spec sections deliberately untouched" §28 bullet claims.

FACT: `DemoteSDK` does remove the registry entry, and it does so SYNCHRONOUSLY inside the RPC (`s.releaseSessionSlot` → `deregisterSlot` + `removeSlotTree`), so the new §5.2 reclaim hold is already over by the time `DemoteSDKResponse` returns and the §4.7-mandated pod-warm fallback bind is not refused by it. EVIDENCE: pkg/adapter/sdkwarm.go:296-302; pkg/adapter/slotsession.go:214-220. This closes the obvious "the hold breaks the demotion fallback" line before someone spends a round on it.

FACT: §15.4.6's conformance categories really do exercise the runtime binary over JSONL against a fake adapter (spec/15_external-api-surface.md:2038-2060), and §15.4.2's DRAINING row is an adapter-PROCESS state, so neither is falsified by the epoch, the outcome enum, or the co-tenant gating. The "deliberately untouched" list is accurate on both.

WATCHOUT: `lenny_adapter_leaked_slots` is cited in spec/05:545 and spec/06:160 and appears in NEITHER spec/16 nor docs/reference/metrics.md nor pkg/alerting/rules. SPEC-3's appended §5.2 text re-cites it ("is surfaced on the `lenny_adapter_leaked_slots` gauge"). This is a PRE-EXISTING inventory hole this proposal neither creates nor widens, so it is not a finding here — but a reviewer who greps the metric name will find the hole and be tempted. EVIDENCE: `grep -rn lenny_adapter_leaked_slots spec/ docs/` returns only spec/05:545 and spec/06:160.

UNVERIFIED: `PrepareWorkspace` is client-streaming and each streamed message is a flat `PrepareWorkspaceRequest` carrying `session_id` (schemas/lenny-adapter.proto:682-696), so under §4.7.1's epochless-admission rule every chunk message of one upload would mint a fresh epoch, and the caller only learns an epoch from the single end-of-stream response. The caller rule ("holds the most recent epoch the adapter reported") still lands the caller on the last minted value, so I could not show a broken fence, but nobody in the log has reasoned about the streaming RPC's per-message admission. A mechanism lens, not an edit-site lens, should decide whether §4.7.1 needs to say the epoch is evaluated once per stream.

USEFUL [review-log.md:108]: the §29.2 steps-2-10 scoping saved me from filing spec/29 step 10 as a missed edit site; it also generalises to spec/15:594, which the log did not name.
USEFUL [review-log.md:363]: "`§15.4.2` is the repo's established citation for the CH-RUNTIMEOPS `terminate` signal" stopped me filing the §4.7 row's §15.4.2 attribution as a false citation.
USEFUL [review-log.md:1417]: the anchor/verbatim sweep is genuinely complete; I spot-checked spec/04:157, spec/06:150-156, spec/07:210-214, spec/07:414, spec/15:1469-1471, spec/29:708 and every replace-target matched byte for byte. Do not re-run it.


### [spec.5.review-feasibility.1]

FACT: THE STAGING DID NOT CHANGE THIS ROUND. `diff -rq scratchpad/cp-snap/0081.../spec-r4` against the live proposal differs ONLY in the review log; `spec-r5` and `spec-r5-start` are byte-identical to live. The last real staging delta is `spec-r3-prefix` → `spec-r4` (the "zero is not an epoch" rule in §4.7.1/§15.4 and the §4.1 rewrite that DELETES the epoch sentence and delegates both teardown preconditions to §4.7). Diff against `spec-r3-prefix`, not `spec-r5`. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/

FACT: `PrepareWorkspace` IS CLIENT-STREAMING and every frame is a full `PrepareWorkspaceRequest`. `rpc PrepareWorkspace(stream PrepareWorkspaceRequest) returns (PrepareWorkspaceResponse)`; the frame carries `session_id`, `upload_ref`, `chunk`, with 4 reserved. The adapter resolves the slot entry ONCE, from the FIRST frame, and later frames are only checked for a non-empty session id. Every per-request rule the epoch blocks state has to say which frame it binds. — EVIDENCE: schemas/lenny-adapter.proto:41,:682-696; pkg/adapter/staging.go:44-47,:66-83. This is the finding I filed.

FACT: `AssignCredentialsResponse` IS an empty message (`message AssignCredentialsResponse {}`), so SCHEMA-1's "its first field is 1" is correct. — EVIDENCE: schemas/lenny-adapter.proto:1033.
CORRECTS [standing context, Traps, "MISTAKE: SCHEMA-1 asserts `AssignCredentialsResponse` is an empty message today"]: that entry says the message holds `session_id = 1`, `leases = 2`, `rotation_trigger = 3`, `coordination_generation = 5`, 4 reserved, and calls `bind_epoch = 1` a hard collision. Those are the fields of `RotateCredentialsRequest` (schemas/lenny-adapter.proto:1035-1057), not of `AssignCredentialsResponse`. Field 1 is free and SCHEMA-1 is right. Do not "fix" SCHEMA-1 to field 6 on the strength of that entry.

FACT: the §4.1 rationale's citation checks out. `ShutdownRequest.coordination_generation` is a bare `int64` field 6 at schemas/lenny-adapter.proto:1630-1635, inside `message ShutdownRequest` (:1609-1636), so "a bare `int64` … present on every `ShutdownRequest`" is accurate and the §4.1 no-engagement argument stands on real ground.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller-rule clause is implementable. `Server.DemoteSDK` calls `noteRuntimeClosed(sessionID)` then `releaseSessionSlot(sessionID)` for the registry's single entry, and the whole thing is synchronous inside the RPC, so the hold it starts is over before the RPC returns and the spec-mandated pod-warm fallback (§4.7 `ConfigureWorkspace` row) cannot meet it. — EVIDENCE: pkg/adapter/sdkwarm.go:274-303; spec/04_system-components.md:673-674.

FACT: spec/18 needs no edit for SPEC-5 either. Its only adapter-contract deliverable lines are the `lenny-adapter.proto` artifact (spec/18:92-93), the Phase-3 `DemoteSDK`/`ConfigureWorkspace` stubs (:184) with the gateway wire-in in Phase 8 (:405), and the `cmd/lenny-compliance` Basic battery per §15.4 (:121-122). None enumerates the bind sequence, an RPC's field set, or an adapter admission rule, so no phase deliverable goes stale and none acquires a later-phase dependency. This extends the settled "SPEC-1 through SPEC-4 need no §18 edit" to SPEC-5; do not re-derive.

DEFERRED [0081….non-spec-changes.md]: SCHEMA-1's field table stages `bind_epoch` on the SEVEN RESPONSES plus `ShutdownRequest.expected_bind_epoch` and `ShutdownResponse.slot_reclaim` — nine fields, none of them a request field on a bind-sequence message. The staged spec now mandates the request side in three places (spec-changes.md:642 "Each of them may carry a bind epoch on its request", :644 "A request carrying an epoch is admitted only when the entry … carries that epoch", :646 the caller echo rule), so the wire form the spec requires is not staged. The remedy is in the schema table at non-spec-changes.md:1029-1044, which this loop may not edit. The standing context already carries this as a Deferred; this shard confirms it against the current table.

DECISION: I did NOT file the §7.1 clause "a further attempt … meets that hold while the reclaim runs and is refused as a transient condition that spends one of the attempts the retry policy allows; [§5.2] keeps the retries its slot retry policy places off that pod" (spec-changes.md:351) as self-contradictory. BECAUSE the standing DECISION on §7.1's retry-exclusion rationale makes the first clause the REASON for the exclusion (the cost §5.2 avoids) rather than a claim that the encounter happens. ALTERNATIVES: filing it as an unreachable trigger state (a policy-placed retry either finds the reclaim complete, so no hold, or finds it incomplete, so §5.2 excludes the pod, and the only reachable encounter is the create-time-reserved client retry, which spends client retries and not the policy budget). A later round tempted by it should read the rationale reading first; it is a wording question at best.

WATCHOUT: the "zero is not an epoch" rule (spec-changes.md:640,:664) is what makes the streaming gap bite. With `bind_epoch` a bare `int64`, every `PrepareWorkspaceRequest` frame after the first carries zero, which the rule defines as "carries none", and the epochless-admission rule then mints a fresh epoch and re-takes ownership on each such frame if an implementer applies the rule per message. Any fix that touches the zero rule must state the per-call scoping in the same edit.


### [spec.5.review-fresh.1]

DECISION: returned an EMPTY findings list — BECAUSE every independent check I ran against the tree
came back clean, and the three candidates I built all died on the materiality bar or on a standing
refutation. ALTERNATIVES: filing the three below; each is written out here so the next lens does not
rebuild them.

FACT: the mechanical anchor sweep is still clean after the SPEC-5 amendment, and here is the script
that proves it in one command. Over the 31 fenced blocks of `.spec-changes.md` against `spec/*.md`:
blocks 0,2,5,8,10,12,14,16,18,20,22,24 have count 1 (the anchors), every other block has count 0
(replacements and insertions). No anchor is non-unique repo-wide. Re-run it, do not eyeball.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md fenced blocks vs spec/*.md.

FACT: every markdown anchor the amendment mints resolves. `#471-role-and-gateway-rpc-contract`
(spec/04_system-components.md:659 `#### 4.7.1 Role and Gateway RPC Contract`),
`#1542-rpc-lifecycle-state-machine` (spec/15_external-api-surface.md:1686),
`#101-horizontal-scaling`, `#47-runtime-adapter`, `#52-pool-configuration-and-execution-modes`,
`#62-pod-state-machine`, `#479-startup-sequence-for-type-agent-runtimes`, `#71-normal-flow`.
Both SPEC-5 insertion points exist verbatim: the `*Adapter → Gateway RPCs:*` table closes at
spec/04:693 with `#### 4.7.2` at :695, and `**SDK-warm demotion contract:**` is
spec/15:1469 with `#### 15.4.1` at :1471.

FACT: the epoch fence actually closes the race it exists for, traced end to end. If attempt A's
`StartSession` was admitted (`st.started` set at pkg/adapter/slotsession.go:88, before
`Runtime.Start` at session.go:156), then admission rule 1 refuses B's epochless bind until A's
reclaim clears the entry, so B CANNOT bind before A's reclaim lands, so the "reclaim tears down a
running successor" ordering is unreachable on that path. If A failed pre-start, B is admitted at a
fresh epoch and A's reclaim answers `superseded`. Do not re-derive this; it holds.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule ("`DemoteSDK` ...
removes the entry, so the pod-warm bind sequence that follows a demotion is epochless") is true
against the tree, not just against the spec. EVIDENCE: pkg/adapter/sdkwarm.go:274-302, the
`s.noteRuntimeClosed(sessionID); s.releaseSessionSlot(sessionID)` pair at :296-298.

FACT: the seven bind-sequence RPCs the amendment names are exactly the entry-creating handlers in
the tree, verified by grep rather than by trusting the list. `ensureSlotPaths` has three callers
(staging.go:134 PrepareWorkspace, :181 FinalizeWorkspace, :337 RunSetup), `ensureSlotStateLocked`
two more (slotcreds.go:26 AssignCredentials, slotsession.go:75 via claimSessionSlot), and
`claimSessionSlot` has three callers (session.go:111 StartSession, resume.go:50 Resume,
sdkwarm.go:217 ConfigureWorkspace). There is no eighth. The §4.7.1 exhaustiveness clause holds.

FACT: `spec/28_communication-channels.md` contains ZERO occurrences of `Shutdown`, so the
"§28's registers" untouched-bullet is true by absence. `spec/15` §15.4.6 really is a RUNTIME-binary
battery against a fake adapter (spec/15:2038-2075, "Start the runtime against the fake adapter"),
so the CONF-1 scope call is right. Both were verified this round; do not re-verify.

FACT: no tier-11 §5.2 gate can be redirected by SPEC-3's append. The anchors in use are
`Whole-pod replacement trigger`, `Session count limit`, `Uptime limit`, "increments `<gateway>`",
`**Slot (session mode).**`, `A service-mode slot is a different thing`,
`The gateway triggers the whole-pod scrub`, `**Fresh-guest reprovision:**`, and
`the pod is held for its tenant through the claim's `reserved` state`. None appears in the appended
text, and `lineContaining` is case-sensitive so the append's lower-case "whole-pod replacement
trigger stated below" does not collide. EVIDENCE: tests/tier11_docs/
concurrent_slot_lifecycle_doc_reconciliation_test.go:92,155,222,298;
slot_definition_glossary_reconciliation_test.go:84,127;
recycle_scrub_trigger_consistency_test.go:70,139,144;
vm_restart_reprovision_consistency_test.go:71,197.

MISTAKE (nearly filed, three dresses — each is refuted here so nobody spends a verifier pair):
(1) "§7.1's hold sentence says a further attempt 'spends one of the attempts the retry policy
allows' while the next clause keeps policy-placed retries off that pod, so the case is unreachable."
It dies because the SPEC nowhere requires the adapter to finish the cleanup before answering
`Shutdown`; only the shipped adapter is synchronous. A conforming adapter that answers
`reclaimed`+clean early leaves the pod un-excluded and the hold still held, which makes the clause
reachable.
(2) "SPEC-4's staged §6.2 paragraph restates the hold's window ('from the adapter's deregistration
... until the cleanup finishes'), contradicting its own rationale at spec-changes.md:624-628 ('§6.2
does not become a third statement of a window §5.2 and §15.4 already fix') and the Design paragraph
at :144-146." Both quotes are accurate and the tension is real, but the APPLIED spec would carry a
redundant-and-consistent restatement, so it fails (c)'s "leaves the spec internally inconsistent"
clause and lands in the barred redundancy category.
(3) "The §4.7 row calls the epoch 'a precondition on the whole request' while its own colon-gloss,
§4.1's replacement, and §15.4 all limit it to the slot release and the runtime teardown, leaving the
whole-pod scrub's gating undecided." Real wording tension, inert in practice (the recycle `Shutdown`
is a separate epochless RPC), and 2:1 against the outlier phrase. Wording, not a defect.

WATCHOUT: the create-time-reserved edge case closes with an ABSOLUTE claim — "Neither the tree the
retry staged nor the session it started can be taken down by the previous attempt's reclaim"
(spec-changes.md:180-181) — and that claim is still false in one sub-case the same document records
separately: a bind that fails inside its first entry-creating RPC holds no epoch and reclaims
unfenced (:196-203). On the create-time-reserved path an upload-free plan reaches
`FinalizeWorkspace` as its first entry-creating RPC, so the sub-case is reachable there. I did NOT
file it, because the material skeptic has already refuted a structurally identical finding on this
same bullet family ("the sentence never lands anywhere ... wording/accuracy quibble in proposal
rationale"), and because standing-context MISTAKE #505 shows the same sentence was already fixed
once by a different route. A round that wants it must argue materiality, not truth.

FACT: every failed §7.3 re-attach reclaims UNFENCED, because `Resume` is the attempt's only
entry-creating RPC, so a failure leaves the caller holding no epoch. This is harmless rather than a
gap: §7.3 retries claim a FRESH pod each time (spec/06:288 "Each retry claims a fresh pod"), so no
successor exists on that pod under that identifier, and §7.2's mid-resume attempt is not retried at
all (spec/07:215). Nobody had written this down; a later lens that notices the resume path is
100% unfenced should stop here rather than file it.

UNVERIFIED: SPEC-4's §6.2 prose scopes the pre-`running` window to "Every earlier stage of the
§4.7.9 step-5 bind sequence", and `Resume` is in no §4.7.9 step-5 sequence, so it is not stated that
a resume-path abandonment leaves the slot in `receiving_uploads`. Under §6.2's own trigger
("workspace materialization begins for this slot") a checkpoint restore is materialization, which
is the charitable reading, and it is the same charitable reading the standing Open on upload-free
plans already assumes. Somebody should settle the two together rather than one at a time.


### [spec.5.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on the spec staging — BECAUSE the staged spec text has essentially no Kubernetes surface left to judge: `grep -in "controller|RBAC|finalizer|admission|status|CRD|informer|watch|etcd|reconcil|SandboxClaim|annotation"` over `.spec-changes.md` returns five hits and none of them is a Kubernetes actor (`admission` at :217,:219 is the ADAPTER admitting an RPC; `CRD validation rule` at :581 is a "stands as written" no-change note; `authoritative-enumeration` at :468 and the §6.2 prose at :621 are state-machine prose). ALTERNATIVES: filing the epoch/hold as a coordination-idiom problem — rejected, both are in-memory, pod-local, never persisted, which is the RIGHT idiom choice for per-request state (etcd is not a per-request database), so the amendment moves the design toward the idiom rather than away from it.

FACT: the whole bind-epoch + reclaim-hold amendment adds ZERO Kubernetes-visible state. No CRD write, no status subresource, no finalizer, no admission webhook, no informer, no work-queue, no leader election. Nothing in SPEC-1..SPEC-5 writes another component's status or uses one as an RPC inbox. EVIDENCE: proposals/.../0081_....spec-changes.md:640-676 (the two SPEC-5 blocks: "held in memory and never persisted"); review-log.md:332 records the same conclusion independently.

FACT: the only three Kubernetes-actor claims in the staged text are all pre-verified and none is new in the amendment: §7.1's "the failed attempt releases the pod's claim and the pod retires under the §6.2 pre-attached failure disposition" (spec-changes.md:351), §7.2 step 3's "released back to the pool via the standard pod release path" (:416), and the §6.2 cancel-bullet clause (:464). Gateway owns `SandboxClaim.spec` and `.status` per §4.6.3 and routes the drain through the `lenny.dev/drain-request` annotation, so no component writes another's status. EVIDENCE: review-log.md:73 (Settled "Ownership is clean"), :488 (`DrainSandbox` is the §4.6.3-conformant route), :246 (no §10.3/§13.1/§13.2 control is touched).

FACT: `spec/10_gateway-internals.md:30` already states "Pods validate the generation on every gateway→pod RPC", and `schemas/lenny-adapter.proto:1629-1635` repeats it in the `ShutdownRequest.coordination_generation` comment. So SPEC-5's sentence "the generation ... is validated on the RPCs that carry it" (spec-changes.md:640) restates shipped spec rather than minting an obligation, even though only `CheckpointBarrier` and `CoordinatorFence` enforce it in code (review-log.md:64,:337). Do not file it as a new obligation on `Shutdown`.

FACT: "Where both appear on one message, as on `Resume` and `Shutdown`" (spec-changes.md:640) is accurate — `ResumeRequest.coordination_generation = 14` and `ShutdownRequest.coordination_generation = 6`. EVIDENCE: schemas/lenny-adapter.proto:1082 (ResumeRequest), :1635 (ShutdownRequest).

FACT: the ordering that would make the §7.1 compensation unsendable if `Shutdown` ever enforced the generation fence does NOT arise on the §7.2 path: staged step 3 is the reclaim and step 4 is the `coordination_generation` bump, so the reclaim precedes the bump. EVIDENCE: spec-changes.md:416 (step 3 replacement); spec/07_session-lifecycle.md:215 (step 4). This narrows review-log.md:337's standing note.

WATCHOUT: `diff -rq` against `spec-r5` and `spec-r4` shows only the review log differing; the last real `.spec-changes.md` delta is r3 → current (the §4.1 epoch-sentence withdrawal and the zero-is-not-an-epoch rules). Diff against `spec-r3`, not `spec-r5`. EVIDENCE: scratchpad/cp-snap/0081_.../spec-r3 vs spec-r4.

WATCHOUT: `SCHEMA-1`'s table at non-spec-changes.md:1033-1041 lists ONE request field (`ShutdownRequest.expected_bind_epoch = 7`) and seven response fields, and `AssignCredentialsResponse.bind_epoch` is still staged at field 1, which review-log.md:512,:594 record as a hard collision (free number is 6). Both are non-spec-lane and OUT OF SCOPE for the spec loop; the request-side echo SPEC-5 mandates for the other six bind-sequence RPCs also has no staged field in that table. The spec-lane fixer must not "fix" these here.

USEFUL [review-log.md Standing context]: the `### Traps` block pre-killed four candidates I would otherwise have spent verifier pairs on — the phantom-`SandboxClaim`/stuck-finalizer dress (:487), `DrainSandbox` from the new `resumeOnPod` caller as a gateway write into controller-owned state (:488), the "adapter cannot see this leak" gauge-attribution dress (:357), and the exclusive-pod-retirement family (:370). Each is exactly the shape a Kubernetes lens reaches for first.


### [spec.5.review-mechanism.6]

FACT: The r5 snapshot set is stale in the usual way. `spec-r4`, `spec-r4-start`, `spec-r5-start` and `spec-r5` are all byte-identical to the live proposal; the last real spec-lane delta is `spec-r3` → live and it is TWO things only: (a) the "zero is not an epoch" rule added to the Design, to §4.7.1 and to §15.4 (three non-conformance clauses), and (b) the §4.1 edit shrunk from "one sentence replaced, one added" to "one sentence replaced", with the epoch sentence deleted and the rationale re-grounded on "the field has no wire presence". EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../spec-r3 proposals/0081_...`

FACT: the whole staged-anchor sweep re-runs clean in one command and is worth the 10 seconds. A python pass over the 31 fenced blocks of `.spec-changes.md` against `spec/*.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24 (every anchor) and count 0 for every replacement/insertion. Nothing has drifted since Settled #232. EVIDENCE: proposals/0081_.../0081_....spec-changes.md fenced blocks vs spec/*.md

FACT: only `ResumeRequest` (field 14) and `ShutdownRequest` (field 6) carry `coordination_generation` among the seven bind-sequence requests plus `Shutdown`; the other six carry none. So §4.7.1's "Where both appear on one message, as on `Resume` and `Shutdown`, each is checked on its own terms" is exhaustive as written, not merely illustrative. EVIDENCE: schemas/lenny-adapter.proto:1635 (ShutdownRequest), ResumeRequest `coordination_generation = 14`

FACT: the SPEC-1 §4.1 rationale's citation `schemas/lenny-adapter.proto:1630-1635` resolves exactly: :1630-1634 is the `coordination_generation` comment and :1635 the bare `int64` field. The "bare `int64`" claim about `expected_bind_epoch` matches SCHEMA-1, which stages `int64 expected_bind_epoch` at field 7 (non-spec-changes.md:1033).

DECISION: I did NOT file the §4.1 "no wire presence" rationale — BECAUSE the CONCLUSION (§4.1 needs no epoch sentence) survives on the rule's own operative qualifier, "standing in for a **scope**": both `Shutdown` forms address one session and ask for the same thing, so the rule is not engaged whatever the presence story is. ALTERNATIVES: filing that the ground is false (the same proposal defines the epoch entirely in presence terms — "a request whose bind epoch is zero carries none" — and §4.1's own untouched clause reasons about presence for `recycle`, "when the recycle disposition is set"). Rejected because the applied §4.1 sentence is not wrong and the ground lives in proposal rationale, which does not clear the bar. A later lens will re-derive this; the reasoning that killed it is here so it is not re-litigated.

DECISION: I did NOT file §4.7.1's caller rule "holds none once it has itself issued an RPC that removes the entry that epoch names" — BECAUSE the only instance the spec names is `DemoteSDK`, and a caller can only apply the rule where the spec states an RPC removes the entry. It reads dangerous, because the shipped adapter DOES remove the entry on several failed bind-sequence branches (session.go:133,:147,:157; resume.go:69..141; sdkwarm.go:236,:251) and a caller applying the rule to those would send the UNCONDITIONAL form on exactly the path the epoch exists for. But the gateway cannot observe it and the staged `adapterclient` latch never zeroes, so the implementation takes the safe reading. Recorded rather than filed.

WATCHOUT: the "zero is not an epoch" rule is consistent everywhere I could reach, and the one place it could have broken is a bind-sequence RPC that answers a SUCCESSFUL response while the adapter holds no entry (which would have to report zero and would then be non-conformant under the new §15.4 clause "An adapter that reports zero on a bind-sequence response does not conform"). There is no such path: `ConfigureWorkspace`'s graceful refusal is returned as a gRPC error rather than as a populated `refusal_reason` response, despite the proto comment on `ConfigureWorkspaceResponse` saying otherwise. EVIDENCE: pkg/adapter/sdkwarm.go:246-252; schemas/lenny-adapter.proto ConfigureWorkspaceResponse comment

FILED: the `superseded` residue claim. §4.7.1 rule 1 admits an epochless successor onto the abandoned attempt's SURVIVING entry ("holds one whose session has not started"), and `ensureSlotStateLocked` returns that same `*slotState` with its `paths`, `creds` and on-disk tree intact, so the successor ADOPTS the predecessor's state. The staged §7.1 paragraph nevertheless says "the state the failed attempt created is already gone" and the staged §4.7 row says "it can hold no live state there". The proposal's own create-time-reserved edge case (spec-changes.md:172-176) writes the adoption out in full. The conclusion (superseded is a completed, unleaked reclaim) is right; the ground is false, and it is false in the primary superseded ordering rather than a corner. EVIDENCE: proposals/0081_....spec-changes.md:283,:351 vs :172-176,:644; pkg/adapter/slot.go:105-125

OPEN: after a `superseded` reclaim the successor is running on the predecessor's workspace tree and staged uploads. §5.2's "Fresh workspace guarantee" assumes a distinct slot (Settled #58) and does not hold there. Nobody has asked whether the applied spec should say so, or whether it is simply the same-session, same-tenant retry semantics the create-time-reserved path already has.


### [spec.5.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE every observability surface the staged spec touches checks out against the tree, and the two remaining wrinkles are pre-existing or already refuted — ALTERNATIVES: filing the docs/reference/adapter-contract.md staleness (deferred, this loop may not edit docs) and filing the §5.2:561 leaked-trigger parenthetical (pre-existing, see MISTAKE-adjacent note below).

FACT: the spec-changes file did not move between rounds 4 and 5. `diff -q spec-r4/*.spec-changes.md spec-r5/*.spec-changes.md` and `diff -rq spec-r5 proposals/0081_...` are both silent, so the "read the newest text hardest" instruction had no newest text to point at this round. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r4, spec-r5.

FACT: no alert in the catalog touches slots, leaks, or session scrubs. `grep -n "slot\|scrub\|leak" pkg/alerting/rules/rules.go` returns nothing across all 158 rules, so this proposal creates no alert-to-metric or alert-to-runbook obligation. Do not spend a round re-deriving it. EVIDENCE: pkg/alerting/rules/rules.go (no match).

FACT: spec/16's observability inventory needs no edit from this proposal. `lenny_slot_failure_total` (spec/16_observability.md:14) declares no closed `error_type` value set, `lenny_slot_pod_replacement_total` (:15) is untouched, `lenny_pod_session_reuse_count` (:128) counts sessions the pod served and is made MORE correct by the withheld pre-`running` report, and `lenny_adapter_leaked_slots` is not in spec/16 at all (it lives at spec/05:545 and spec/06:160). EVIDENCE: spec/16_observability.md:14,15,128; spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:160.

FACT: the withheld cleanup-outcome report does NOT falsify the two spec sites that state what `ReportSessionScrub` drives. spec/04:692 says "Report the outcome of the per-slot cleanup at a session release ... The gateway increments `sessionsServed`", and spec/12:481 says `sessions_served` is "incremented at each session release (`ReportSessionScrub`)". The staged §5.2 append deliberately frames the pre-`running` cleanup as something that "ALSO runs" beside a session release rather than as one, so both sites stay true verbatim and neither is a missing edit site. This is the framing that saves the proposal here; a fixer who reworded that clause to call the pre-`running` cleanup a session release would break both. EVIDENCE: spec/04_system-components.md:692; spec/12_storage-architecture.md:481; spec-changes.md:562-566.

WATCHOUT: §5.2's whole-pod replacement trigger glosses `leaked` as "(cleanup timeout exceeded)" while §5.2's own `**Slot cleanup:**` bullet forty lines earlier says "If cleanup fails, the slot is leaked". The staged text adds a third leak trigger (a reclaim unanswered, or answered `reclaimed` without a clean exit) and points at "the whole-pod replacement trigger stated below". I did not file this: the narrower parenthetical already disagrees with the broader bullet before this proposal applies, and the shipped code already marks a slot leaked on a failed reservation release rather than on a timeout. It is a pre-existing imprecision the proposal leans on rather than one it creates. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561 versus :545; BUILD-GAPS.md:4963.

DEFERRED [docs/reference/adapter-contract.md]: the `Shutdown` row at docs/reference/adapter-contract.md:75 becomes stale once SPEC-1's §4.7 row lands, and that file is in no edit list (the docs lane stages DOCS-1 for docs/reference/state-machines.md only, non-spec-changes.md:1090). What is now false: the row states one unconditional sequence ("The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`"), where the staged §4.7 row splits it into a slot release gated on the adapter holding an entry and a runtime teardown gated on the adapter having admitted the session's start; the row states the cleanup-outcome report unconditionally, where the staged §5.2 scrub-model append withholds it on the pre-`running` path; and the row carries neither the bind-epoch precondition nor the `reclaimed`/`superseded`/`absent` outcome triple. The sibling `ReportSessionScrub` row at :81 ("at each session release, on a pod of any concurrency") stays TRUE, because the staged text keeps a pre-`running` cleanup outside the term "session release" — correct only that row 75. EVIDENCE: docs/reference/adapter-contract.md:75,81; spec-changes.md:242 (staged §4.7 row), :562-566 (staged §5.2 append).

FACT: §5.2's slot-cleanup action list has no docs mirror. `grep -rn "process group\|slotId\|slot's workspace directory" docs/` returns nothing, so SPEC-3's first anchor (adding the credential directory and the §4.9 expiry timers to the action list) creates no docs edit site. EVIDENCE: docs/ (no match).

USEFUL [Standing context, "The `leaked` disposition already exists end to end" and "Dead end: the adapter cannot see this leak"]: both saved me from re-filing the gauge-ownership angle. `lenny_adapter_leaked_slots` is registered and written by the GATEWAY (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:223) despite §6.2:160 calling it adapter health metadata, so withholding the adapter's report blinds nothing. Keep this entry.


### [spec.5.review-performance.3]

DECISION: returned an empty findings list for the performance / scalability / failure-mode lens on this (post-amendment) spec.5 staging — BECAUSE the two mechanisms the hand amendment added (bind epoch, reclaim hold) add no store writes, no new watches and no shared serialization point, and every failure-mode candidate I developed is either pre-existing, already refuted, or a close variant of a declined item — ALTERNATIVES: the four candidates recorded below, each worked to a conclusion and dropped.

WATCHOUT: `diff -ru scratchpad/cp-snap/0081_.../spec-r5 proposals/0081_...` is EMPTY again this round, exactly as it was for the pre-amendment `[spec.5.review-performance.2]`. spec-r4 and spec-r5 are byte-identical too (md5 f8dea4c4d24c7e3d3dbbe9a7adec7ca6). To see what the last fix round actually changed, diff spec-r3 against spec-r5: it is the single §4.1/zero-epoch group (the epoch declared a positive integer so a zero field carries none; §4.1's replacement sentence shortened to delegate both preconditions to §4.7 and state nothing about the epoch; the matching non-conformance clauses in §15.4). That group is the newest text in the document. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r{3,4,5}.

FACT: the write-rate half of this lens is still empty AFTER the amendment, and a future round need not redo it. The bind epoch is one `int64` per adapter registry entry plus one per-connection latch on the gateway client; the reclaim hold is a pod-local set of slot identifiers. Neither is persisted, neither is a Redis key, neither is an etcd object, and neither adds a reconcile or a watch. The epoch travels as an existing-message scalar on RPCs the bind sequence already sends, so it adds zero RPCs. The only added call remains one `Shutdown` per FAILED bind on an already-open connection. `[spec.5.review-performance.2]`'s Redis math (`ReleaseSlot(leaked=true)` early-returns before the decrement, so writes go down rather than up) is unaffected by the amendment. EVIDENCE: spec-changes.md:596 ("pod-local, and never persisted"); spec-changes.md:594-600; non-spec-changes.md:950-963.

FACT: neither new mechanism serializes anything pod-wide. The hold is keyed on the slot identifier (`SlotID == SessionID`), so N concurrent cleanups on one pod refuse N distinct identifiers and never each other, and §5.2's `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` divisor already prices overlapping cleanups. There is no hot key and no single-leader step anywhere in the amendment. EVIDENCE: spec-changes.md:554 (reclaim-hold paragraph); spec/05_runtime-registry-and-pool-model.md:545 (the formula).

MISTAKE (nearly filed, do not re-derive): "after a coordinator handoff the stale replica's compensating `Shutdown` is generation-rejected (spec/10:30, 'Pods validate the generation on every gateway→pod RPC'), so §7.1's reclaim obligation is unsatisfiable on the handoff path and the slot leaks." Two things kill it. The shipped adapter validates `coordination_generation` only in `CoordinatorFence` and the barrier path, nowhere in `Shutdown` (grep for `GetCoordinationGeneration` under `pkg/adapter/` returns coordination.go:120 and :262 only), so the code does not reject it; and even if it did, the outcome is the `leaked` disposition §7.1 already states for an unanswered reclaim, which is no worse than the shipped behaviour of sending no reclaim at all. EVIDENCE: spec/10_gateway-internals.md:30; pkg/adapter/coordination.go:120,:262.

MISTAKE (nearly filed, do not re-derive): "the new pre-`running` leak class loses its occupancy on a Redis reset, because §5.2's rehydration rebuilds the counter from `SessionStore.GetActiveSlotsByPod` and a failed bind never has an active row." The fact holds, but `[spec.5.review-reliability.1]`'s CORRECTS entry already reached it, judged the inconsistency to predate 0081, and located the fix in §5.2's rehydration paragraph rather than in anything 0081 stages. Filing it here would be that declined item under a different lens.

MISTAKE (nearly filed, do not re-derive): "the amendment raises the pod-replacement rate at the top tier, because CODE-5 extends leak accounting to the create-time-reserved and §7.3 re-attach paths and at `maxConcurrentSessions: 2` one leak drains a pod." True as arithmetic (`UnhealthyThreshold = (maxConcurrent+1)/2 = 1`), but the proposal states it in writing and it is a conformance gap closing: those paths reach no tracker today, so the slots leak invisibly instead of cheaply. Counting a resource that genuinely leaked is not a new bottleneck. EVIDENCE: non-spec-changes.md:786-800; standing context Settled ("The churn change starts at concurrency 3").

USEFUL [Standing context, Open: "Nothing in the staged spec bounds the reclaim hold on an exclusive pool" and the refuted "The reclaim hold is given a bound its own cited source does not provide"]: the exclusive-pool hold bound is where this lens naturally goes first and both entries are needed to see why it is closed. SPEC-3's appended pointer clause ("That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency") is what carries the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` figure out of the `maxConcurrentSessions > 1` heading, and the reclaim-hold paragraph's last sentence then cites it. Do not re-file it. EVIDENCE: spec-changes.md:552,:554.


### [spec.5.review-reliability.2]

DECISION: returned an EMPTY findings list for the spec staging under the reliability lens — BECAUSE every recovery-path hazard I could construct against the staged text either resolves clean against the tree, is recorded in the proposal's own accepted-failure-mode list, or is already a Standing Trap / barred close variant — ALTERNATIVES: I built and dropped five candidates, each recorded below so nobody rebuilds them.

FACT: the hold / exclusion / epoch trio is coherent under retry, and here is the trace that shows it. `compensateFailedSlotBind` BLOCKS on `cl.Shutdown` (Standing Settled), and CODE-6 releases the hold with `defer s.releaseReclaim(sessionID)` at the `Shutdown` handler's return, i.e. before the response is written. So by the time the gateway can place a retry it has either (a) the answer, in which case the hold is already over, or (b) a deadline expiry, in which case the reclaim "did not complete", `ExcludePods` fires and the retry goes to another pod. There is no ordering in which a policy-placed retry meets a live hold on the pod it was just placed on. Only the create-time-reserved client retry can, and that is the recorded residue. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:185-188; spec-changes.md:182-195

FACT: on the §7.3 re-attach the compensation is ALWAYS unfenced, and it is safe. `Binder.Resume` issues exactly one entry-creating pod-side RPC, `cl.Resume`, after `b.connect` and `reserveResumeSlot`; there is no earlier bind-sequence RPC on that connection to report an epoch. So a `Resume` that fails or whose response is lost leaves the attempt holding no epoch and it sends the unconditional form — and the residue class there IS a started session, because the adapter's `Resume` reaches `claimSessionSlotUnderLock` and `Runtime.Start`. It is nonetheless safe, because each §7.3 retry claims a FRESH IDLE pod through `podclaim.Claimer.Claim` and never returns to the reclaiming pod, so the lagging unconditional reclaim can meet no successor. A later round tempted to widen the "one RPC wide" residue bullet onto the resume path should stop here. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1622; pkg/adapter/resume.go:50,:140-144

MISTAKE nearly filed: "§4.7.1's 'the generation ... is validated on the RPCs that carry it' and 'on `Resume` and `Shutdown`, each is checked on its own terms' assert a check that does not exist, and if implemented would refuse the §7.1 compensation exactly when a coordinator handoff has bumped the generation." Both halves of the premise are true against the code (`grep -rn "GetCoordinationGeneration" pkg/adapter/*.go` returns pkg/adapter/coordination.go:120 and :262 and nothing else; `adapterclient` sets `CoordinationGeneration` only on `CheckpointBarrier` at client.go:470, never on `ShutdownRequest` or `ResumeRequest`, so both always carry zero). It dies because spec/10_gateway-internals.md:30 already says "Pods validate the generation on every gateway→pod RPC", so §4.7.1 restates a published spec rule and the code gap is pre-existing and unstaged. Two earlier shards reached the same verdict. — EVIDENCE: spec/10_gateway-internals.md:30; pkg/adapter/coordination.go:120,:262; pkg/gateway/runtime/adapterclient/client.go:470,:617-630,:813-818

MISTAKE nearly filed: "the staged hold covers every deregister-and-remove-the-tree, but CODE-6 takes it only in `Shutdown`, so §15.4's 'An adapter that admits a bind onto an identifier whose cleanup is still running does not conform' is falsified by the proposal's own reference adapter." True as stated (sixteen production sites reach `releaseSessionSlot`, which is `deregisterSlot` then `removeSlotTree` with `s.mu` released between them), and an earlier shard at review-log.md:1478-1497 derived it and explicitly declined to file on the standing "the adapter implements no process-group kill ... §5.2's action list is ahead of the code; pre-existing" precedent. Not re-filed. It remains decidable inside the spec lane (scope the hold's antecedent to the `Shutdown` slot release) if a human wants it decided. — EVIDENCE: pkg/adapter/slotsession.go:214-220; pkg/adapter/session.go:133,:147,:157; pkg/adapter/resume.go:69-141; pkg/adapter/sdkwarm.go:236,:241,:251,:298

MISTAKE nearly filed: "a `superseded` reclaim is classified unleaked, so the gateway releases the Redis reservation while the successor's live session runs on it — transient over-assignment against §5.2's Slot assignment atomicity." Real, and pre-existing rather than staged: `BindReservedSlot` already calls `ReleaseSlotReservation` on failure today with no compensation involved, and the client retry reconnects to the persisted `row.PodAssignment` and re-reserves nothing, so the counter already under-counts on exactly this path. Standing Settled records it ("The retried start runs on occupancy the counter no longer counts. Pre-existing and outside this proposal."). This also disposes of the standing Open "Transient over-assignment on the acknowledged racing-start ordering", which this lens raised in an earlier round: the baseline is the same.

MISTAKE nearly filed: "the epoch counter restarts after an adapter container restart, so entry 1 of the new process reuses value 1 and a lagging reclaim compares equal." CODE-6 seeds `nextBindEpochLocked` lazily from `time.Now().UnixNano()`, which is stronger than the spec's "strictly increasing within one adapter process" requires, and the restart kills the gRPC connection the reclaim is required to ride (§7.1's no-re-dial rule), so the reclaim cannot be sent at all. Closes standing Open "Can an adapter restart produce an epoch collision?". — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md, CODE-6 `nextBindEpochLocked`; spec-changes.md:351 (the no-re-dial sentence)

WATCHOUT: `diff -rq` against `scratchpad/cp-snap/0081_.../spec-r5` AND `spec-r4` are both byte-identical to the live proposal. The newest real delta is `spec-r3` → live, and it is 102 diff lines: the "zero is not an epoch" sentences in the §4.7.1 and §15.4 blocks and in the Design narrative, plus the §4.1 rewrite that deletes the epoch sentence from the §4.1 replacement and re-grounds the rationale on `expected_bind_epoch` having no wire presence. Diff against `spec-r3`, not `spec-r5`. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/

FACT: the new §4.1 rationale's citation is exact. `schemas/lenny-adapter.proto:1635` declares `int64 coordination_generation = 6;` on `ShutdownRequest` as a bare proto3 scalar with the comment at :1630-1634, so "carried as the `coordination_generation` fence on the same message already is" and the no-wire-presence argument both hold. — EVIDENCE: schemas/lenny-adapter.proto:1609-1636


### [spec.5.review-security.1]

DECISION: returned an empty findings list for the security lens on the spec staging — BECAUSE every control I could name is either untouched or strengthened by the staging, and the two candidates I built (fail-open epoch default, credential residue on the adoption path) are same-tenant/availability concerns rather than control regressions — ALTERNATIVES: filing the "zero bind epoch is the permissive form, so a caller that forgets the field gets the unconditional teardown / the adoption admission" as a fail-open default; rejected because the fence protects a live session from an errant teardown (availability), zero-as-escape-hatch is deliberate and stated in §4.7 and §4.7.1, and "merely less strict" is barred.

FACT: the snapshot at scratchpad/cp-snap/.../spec-r5 is BYTE-IDENTICAL to the current proposal directory except the review logs (`diff -rq` returns nothing). Round 5 got no fix-stage text to read hardest. Do not spend time on the diff step for this round's successors unless the snapshot is refreshed. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r5

FACT: SPEC-3's two "shipped adapter behaviour" claims for the widened `**Slot cleanup:**` action list both check out. `RemoveTree` removes `p.slotRoot()`, `p.Sessions`, `p.Artifacts`, and `p.CredentialsDir` (= `/run/lenny/slots/{sessionId}`), and `deregisterSlotLocked` loops `st.timers` calling `cancelSlotExpiryTimerLocked` before `delete(s.slots, ...)`. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69; pkg/adapter/slotsession.go:174-182.

FACT: the §4.9 direct-mode expiry timer IS the enforced lease deadline ("its expiry deletes the provider's credential-file entry"), so cancelling it at deregistration drops the enforced deadline while `credentials.json` is still on disk for the length of the cleanup (the shipped handler removes the tree only after the drain and `Runtime.Close`, deliberately). This is pre-existing behaviour that SPEC-3 only documents, and §5.2 scrub step 0 is the backstop. Recorded so a later security round does not re-derive it as new. EVIDENCE: spec/04_system-components.md:1169,:1466; pkg/adapter/session.go:262-271; spec/05_runtime-registry-and-pool-model.md:461.

FACT: the leaked-slot bound that drives the whole-pod replacement trigger is gateway-maintained, not a pod self-report. `slotstate.Registry.MarkLeaked` seeds and counts per pod and returns the count the gateway publishes as `lenny_adapter_leaked_slots`. That is why SPEC-3 routing the pre-`running` accounting through the `Shutdown` response (rather than through the withheld `ReportSessionScrub`) does not move a security bound onto an untrusted source. EVIDENCE: pkg/sandbox/slotstate/registry.go:10-19,:90-109.

FACT: the §11.4 full-revoke fan-out sends `Shutdown` both locally and cross-replica over Redis pub/sub, so the peer replicas dial their own connections and hold no epoch. The staged escape hatch ("a request carrying no epoch is the unconditional teardown") is what keeps that mandatory security control intact; anyone proposing to make the epoch mandatory on `Shutdown` breaks §11.4. EVIDENCE: spec/11_policy-and-controls.md:263,:270; spec-changes.md:283.

WATCHOUT: `spec/06_warm-pod-model.md:283` ("the pod is marked `failed` and released back to the pool") reads like it falsifies the proposal's repeated "on a one-session pod the residue dies with the pod". It does not: the §6.2 fence and §4.6.1 projection govern, and a terminal claim disposition of `released` or `failed` projects `draining` then `terminated`. This was already adjudicated in an earlier pass. EVIDENCE: spec/06_warm-pod-model.md:80,:283; review-log standing context entry 92.

UNVERIFIED: whether the successor that ADOPTS an abandoned attempt's surviving entry (the epochless admission arm, "holds one whose session has not started") inherits that entry's still-armed §4.9 expiry timers, since no cleanup and no `deregisterSlotLocked` runs on adoption. Same session and same tenant, so it is not an isolation issue, but a stale timer firing `AUTH_EXPIRED` at the successor is a correctness question for the code lane. EVIDENCE: spec-changes.md:646-648; pkg/adapter/slotsession.go:174-182.


### [spec.6.fix-G1.1]

DECISION: the §7.1 clause no longer charges a reclaim-hold refusal to the §5.2 slot-retry budget — BECAUSE the same sentence's placement half keeps policy-placed retries off the reclaiming pod, so a policy-placed retry never meets the hold, and the edge case at spec-changes.md "A retry that meets the reclaim hold spends an attempt on it" already settled that what can meet it (a §7.4 upload, a §7.3 resume, a client §15.1 retry on a create-time-reserved slot) is placed by neither mechanism — ALTERNATIVES: keeping the budget clause and deleting the placement half (orphans the staged §5.2 `**Max retries:**` edit); re-attributing the cost to the client's budget inside §7.1 (drags an accounting rule into a section the proposal keeps free of one); enumerating the three attempt kinds in §7.1 (duplicates the edge case, two copies drift).

CORRECTS [standing context, "DECISION: §7.1's retry-exclusion rationale is the reclaim hold, not tree reuse"]: that entry ends "it is refused transiently and spends one of the retry policy's attempts, which is the cost the exclusion avoids". The second half is retired. The exclusion's cost is not a spent §5.2 attempt, because the exclusion is exactly what stops a policy-placed retry from ever reaching the hold. What is true: the hold refuses transiently, §5.2 keeps policy-placed retries off that pod, and an attempt that meets the hold is one no policy placed.

DECISION: the exclusivity sentence "This is the only rule under which the adapter refuses a request on the bind path" is deleted from §5.2 rather than narrowed, and its two pointer restatements in §4.7.1 and §15.4 are re-scoped to the block they sit in — BECAUSE the shipped adapter already refuses on the pod-not-idle, started-session and unresolvable-slot conditions (pkg/adapter/slotsession.go:63-88), §10.1's hold state refuses every non-`CoordinatorFence` RPC (spec/10_gateway-internals.md:57), and §15's setup catalog fixes a `FailedPrecondition` on `RunSetup` — ALTERNATIVES: the reviewer's narrowing, which would have imported into normative §5.2 a partial list of refusals that no spec section states today (pod-not-idle and started-session have no spec statement anywhere), minting normative surface nobody reviewed; scoping the claim to "the only rule in this proposal", meaningless once landed in spec/05.

WATCHOUT: two adapter refusals the code returns on the bind path have NO statement in spec/ at all — the SDK-warm "pod is not idle" refusal and the "session has already started on this pod" refusal — EVIDENCE: pkg/adapter/slotsession.go:69-72 and :82-85; spec/04_system-components.md:672's `StartSession` row states no refusal. Two of this proposal's edge cases lean on the started-session refusal surviving. Do not "fix" that gap inside this proposal by enumerating them in §5.2 or §15.4; it is a separate finding.

FACT: `removeSlotTree` takes no `context.Context` on either cleanup path and runs AFTER the bounded `Runtime.Close`, so no named window bounds the slot-tree removal — EVIDENCE: pkg/adapter/slot.go:208-212 (`func removeSlotTree(st *slotState) error`); pkg/adapter/session.go:262-271 (`Runtime.Close` under `contextWithGraceDeadline`, then `_ = removeSlotTree(st)`); pkg/adapter/holdstate.go:200-201 and :254 (the 10s `closeCtx` bounds only the close). The `Shutdown` handler blocks on that removal before building its response (session.go:271-291), so the gateway's RPC deadline cannot be claimed to always observe the reclaim's real outcome.

DECISION: §5.2 now says the named windows bound the close of the session on the shared runtime process and the directory removal runs after it under no deadline, so the hold outlasts the window by that removal; the three per-caller bound sentences in CODE-1 and the two "reads the reclaim's real outcome" sentences (CODE-4 doc comment, summary give-up-bound bullet) were narrowed to match — BECAUSE the edge case and CODE-1's own `RemoveAll` sentence already stated the truth and only the outliers needed aligning — ALTERNATIVES: giving `removeSlotTree` a context and cancelling it (an interrupted `RemoveAll` leaves the partial tree this proposal exists to prevent); deleting the bound sentence (§6.2 and §15.4 both point at §5.2 for the window); a new accepted-failure-mode bullet for a slow removal (§7.1 already accounts an unanswered reclaim leaked).

WATCHOUT: the fix's replacement sentence as designed opened with "Those windows bound the close ..." with no antecedent, because the sentence naming them had been rewritten. Applied as "The cleanup's close ... is bounded by the graceful window ..., by that request's own deadline ..., and ... by the termination window ...". A later reword must keep the windows attached to the close rather than to the hold — EVIDENCE: spec-changes.md, the `**Slot-identifier reclaim hold.**` block.

FACT: non-spec-changes.md's "No in-gateway wait-and-retry is staged for the hold" paragraph and the CODE-1 `RemoveAll` sentence both already carry the correct "plus the removal of the slot's directories" form and were deliberately left untouched. Rewording them is unreviewed churn.


### [spec.6.fix-G2.1]
DECISION: Scoped the SPEC-5 §4.7.1 caller rule so that only a compensating `Shutdown` for an abandoned bind attempt carries an epoch, and every other `Shutdown` (ordinary session end, occupancy-zero recycle, §11.4 revoke fan-out) carries none and is the unconditional teardown — BECAUSE the old universal sentence ("A caller names the epoch it holds on a `Shutdown` for that session and names no other") obliged every caller riding the bind connection to fence, and those connections hold a non-zero latched epoch, so it contradicted the §4.7 `Shutdown` row, the §7.1 reclaim paragraph, the Design narrative, summary.md and CODE-6's zero-epoch call sites — ALTERNATIVES: fencing every epoch-holding caller (deletes the escape hatch, rewrites `Binder.ReleaseSlot`, the recycle edge and the revoke fan-out, and makes the ordinary session end refusable as `superseded`); keeping the universal rule plus a named exception list inside spec text (mints a closed enumeration that goes stale on a fourth caller).
FACT: The over-broad sentence existed at exactly one site in the whole proposal directory. Every other statement of the rule was already compensation-scoped, so the fix cascaded nowhere. EVIDENCE: spec-changes.md:680 (the only site), :321 (§4.7 row, already scoped), :389 (§7.1, already scoped), :117 and :127 (Design narrative, already scoped).
WATCHOUT: The §4.7.1 caller-rules paragraph now carries three distinct rules in one block: the per-connection latch bookkeeping, the compensation-only epoch rule, and the same-connection/two-connection compensation rules. A later edit that rewrites the paragraph must keep all three; the latch bookkeeping is load-bearing for CODE-6's atomic latch and its two clearing sites (`DemoteSDK`, a `reclaimed` `Shutdown`). EVIDENCE: spec-changes.md:680.


### [spec.6.fix-G3.1]

DECISION: closed the two dangling "open decision D1's Option B" citations by declaring the shared-entry residue ACCEPTED and unstaged, minting no decision number, and stating the closing cost descriptively exactly once — BECAUSE the hand amendment deliberately retired entry 9 and summary.md:326-329 tells the human reviewer so, while stating that resolved entries are deleted and survivors keep their numbers; re-opening a numbered or lettered decision would contradict that preamble — ALTERNATIVES: adding a new entry 12 with Option A/B (rejected: contradicts the preamble and introduces a lettered scheme the proposal never uses); reviving entry 9 (rejected: would mean the epoch and hold amendment did not close what it says it closes); deleting both trailing sentences and stating no closing cost (rejected: every sibling residue bullet states its cost, and the cost is what a human sign-off weighs); stating the full cost in both bullets (rejected: one cost stated twice in one section drifts).

FACT: the closing cost is stated once, in the bullet that owns the shared-entry residue ("A compensation still on the wire when a retry adopts the surviving entry is not fenced", spec-changes.md around :244-249). The earlier create-time-reserved bullet now points at it with "That ordering is the shared-entry residue the accepted failure modes record below, where what closing it would cost is stated." EVIDENCE: spec-changes.md:188-189 and :243-248.

FACT: the design brief attributed the :188-189 sentence to the bullet "A compensation for a session the adapter holds nothing for" (spec-changes.md:152). It is actually the last sentence of "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot" (:166). The anchor string was right and the replacement text fits either way, so the edit landed unchanged. EVIDENCE: spec-changes.md:152,:166,:188.

WATCHOUT: after this round the only surviving "open decision" strings outside the review log are summary.md:104 (past-tense closure prose in `## Decisions`, correct as written) and summary.md:324 (the section heading). Entry 11 is the sole open decision. Any later round that reintroduces a decision citation into a staged change file breaks the standing rule that staged files never reference an open decision. EVIDENCE: `grep -ni "open decision" *.md | grep -v review-log` returns exactly those two lines.

WATCHOUT: the bind epoch does NOT discriminate two attempts that share one surviving registry entry, because a retry adopting a surviving entry inherits that entry's epoch. Do not write prose implying otherwise; that inheritance is exactly why the shared-entry residue exists. EVIDENCE: spec-changes.md:244-248.


### [spec.6.fix-design-G1.1]

DECISION: The reclaim hold's three contested claims are settled once, as one paragraph rewrite at spec-changes.md:601 plus its mirrors — (a) the hold refuses only what the hold refuses and displaces no refusal the adapter already returns, (b) a retry meeting the hold spends no §5.2 `sessionPolicy.slotRetries` attempt, (c) the named windows bound the runtime close alone and the hold outlasts them by the unbounded slot-tree removal. BECAUSE all three are prose corrections that make the paragraph agree with the edge case at :191-201, which the round-6 pass already got right, and with the shipped tree. ALTERNATIVES: giving `removeSlotTree` a context so the stated bound becomes true (rejected: a code change to shipped teardown outside this proposal's deliverables, and an interrupted `RemoveAll` leaves a half-removed tree the next bind would inherit); adding an accepted-failure-mode bullet for a removal that outruns the gateway RPC deadline (rejected as hair: §7.1 already accounts an unanswered reclaim `leaked`, so the residue has a home); enumerating the three attempt kinds that can meet the hold inside §7.1 (rejected: the edge case owns that list, and §7.1 must state no placement rule).

FACT: `removeSlotTree` takes no `context.Context` on either cleanup path and runs after the bounded close. EVIDENCE: pkg/adapter/slot.go:208-212 (`func removeSlotTree(st *slotState) error`); pkg/adapter/session.go:262-271 (`Runtime.Close` under `contextWithGraceDeadline`, then `_ = removeSlotTree(st)`); pkg/adapter/holdstate.go:200-201 and :254 (10s `closeCtx` bounds the close only). So no layer enforces the "in each case the bound also covers the removal" clause.

FACT: the adapter already refuses on the bind path for three reasons unrelated to the hold, so any "only rule" claim is false. EVIDENCE: pkg/adapter/slotsession.go:69-72 (`Unavailable` pod-not-idle), :76-78 (`InvalidArgument` unresolvable slot), :82-85 (`Unavailable` session already started); spec/10_gateway-internals.md:57 (coordinator hold rejects all non-`CoordinatorFence` RPCs with `UNAVAILABLE`).

WATCHOUT: non-spec-changes.md:128-131 and :890-894 ALREADY state the bound correctly ("plus the removal of the slot's directories"). Only :134, :139 and :142 — the three per-caller sentences immediately after :131 — state it wrongly. Do not rewrite the two correct ones. EVIDENCE: non-spec-changes.md:128-131, :134, :139, :142, :890-894.

WATCHOUT: the §7.1 sentence at spec-changes.md:389 is self-contradictory within one clause, not merely wrong: it charges a §5.2 retry-budget attempt AND says §5.2 keeps policy-placed retries off that pod. Deleting the budget clause is the whole fix; do not instead delete the placement half, which SPEC-2's staged `**Max retries:**` edit at :529 implements.

OPEN: spec/04_system-components.md:672's `StartSession` row states no refusal, while `claimSessionSlotUnderLock` refuses a repeat start onto a started session with `Unavailable`. That is a pre-existing spec gap this proposal neither causes nor repairs, and the proposal depends on that refusal twice (spec-changes.md:175-178, :226-227). Worth filing as its own finding in a later round.


### [spec.6.fix-design-G2.1]

DECISION: Scope the §4.7.1 caller-rules obligation to the compensation and change nothing else — BECAUSE the over-broad sentence at spec-changes.md:681 ("A caller names the epoch it holds on a `Shutdown` for that session and names no other") is the ONLY site in the whole proposal that states the universal form; every other statement of the rule is already scoped correctly, so a one-sentence-pair narrowing makes the document consistent with no cascade. — ALTERNATIVES: (a) change the §4.7 row / CODE-6 in the other direction so every epoch-holding caller sends the fenced form — rejected: it deletes the escape hatch, makes the ordinary session-end release and the §11.4 revoke fenced, and rewrites call sites CODE-6 explicitly keeps at a zero epoch; (b) enumerate the three unfenced callers inside the §4.7.1 spec text — rejected as a closed enumeration in a spec block that goes stale on a fourth caller, and the §4.7 row already carries the "every caller other than a fenced compensation" formulation; (c) delete the latch bookkeeping sentences ("A caller holds the most recent epoch...", "It holds none once...") and state the rule only on the compensation — rejected: CODE-6's per-connection atomic latch, its `DemoteSDK`/`RECLAIMED` clears, and the zero-latch `podRegistry.Put` case all depend on that bookkeeping being normative.

FACT: SPEC-2's §7.1 paragraph ALREADY states the scoped form verbatim — "The reclaim names the registry entry it compensates by carrying the bind epoch the attempt observed on that connection ..., and it may name no other. An attempt that holds none ... sends the unconditional form." So the corrected §4.7.1 sentence is a mirror of shipped-in-this-proposal text rather than new design. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:389.

FACT: the Design narrative at spec-changes.md:114-117 states the latch rule WITHOUT the universal obligation ("...so a caller that holds none sends the epochless form"), and summary.md:50 already says "A compensating `Shutdown` names the epoch its own ...". Neither needs editing. EVIDENCE: spec-changes.md:114-117; summary.md:50.

FACT: the code the proposal stages is unambiguous that the ordinary release is unfenced — `cleanly, err := result.Adapter.Shutdown(ctx, result.SessionID, "", 0)` is the session-end teardown on `result.Adapter`, the bind connection, and CODE-6 keeps that signature with a zero epoch. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542; non-spec-changes.md:996-998.

WATCHOUT: the §4.7.1 block is one fenced quote and G1 edits the paragraph immediately above it (the "and by nothing here" clause at :679). Anchor this edit on the quoted sentence pair rather than on a line number, and apply after G1. EVIDENCE: spec-changes.md:679-681.

WATCHOUT: do NOT touch the §4.7 `Shutdown` row at spec-changes.md:321 or its commentary at :324-327 for this finding. Both are already true under the narrowed rule, and the group brief's expectation of a cascade into them does not survive reading them. EVIDENCE: spec-changes.md:321,:324-327.


### [spec.6.fix-design-G3.1]

DECISION: The shared-entry residue is ACCEPTED and unstaged, and no lettered or numbered decision is minted for it. Both "open decision D1's Option B" clauses (spec-changes.md:189-190, :244-246) are replaced with a descriptive statement of what closing would take (a per-ATTEMPT discriminator carried on every request that can create or resolve a slot entry, which this proposal does not stage), matching the idiom the neighbouring bullets already use ("which is a wider change to the gateway-adapter error surface than this proposal stages"). BECAUSE the summary's open-decisions preamble already states entry 9 is resolved by the epoch and the hold "together with the residues that survive", and adding a decision back would contradict it and re-open a human sign-off the amendment deliberately closed. ALTERNATIVES: (a) add a new numbered open decision 12 for the residue — rejected, it re-opens a closed question and the residue is explicitly bounded and recorded; (b) renumber the deleted entry 9 back in — rejected, summary.md:323-329 states resolved entries are deleted and survivors keep their numbers.

FACT: The epoch is minted per admitted bind ATTEMPT but is stored on the registry ENTRY, so a retry that ADOPTS a surviving entry inherits that entry's epoch. The epoch therefore discriminates entry generations, and not two attempts sharing one entry. That is exactly why the shared-entry residue survives, and any replacement prose must say so rather than implying the epoch already covers it. EVIDENCE: spec-changes.md:180-190, :236-246.

WATCHOUT: summary.md:464 and :466 cite "open decision 9" in the PRESENT tense ("asks about", "records") for the same per-attempt-identity gap, while summary.md:325-328 says entry 9 is deleted from the section. Both must be reworded in the same edit or the fix leaves the identical dangling citation one file over. summary.md:104 ("Open decision 9 is closed by fencing the reclaim ...") is past-tense closure prose in `## Decisions` and stays as it is. EVIDENCE: summary.md:104,:325-328,:464-466.

WATCHOUT: G1 rewrites the same `## Edge cases and accepted failure modes` section and the two D1 bullets describe near-identical orderings (a reclaim tearing down a session on a surviving entry a retry adopted). If G1 merges them, apply the descriptive closing-cost sentence ONCE rather than twice; do not merge them as part of G3.

UNVERIFIED: whether the two bullets at spec-changes.md:176-190 and :236-246 are genuinely distinct orderings or one ordering stated twice. A later round should decide; G3 deliberately did not restructure the section.


### [spec.6.review-applicability.1]

FACT: Every staged spec anchor in spec-changes.md verifies clean against the tree at this
commit. Confirmed one by one: spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown`
row opening), spec/04:688/695 (SPEC-5 insertion point, `*Adapter → Gateway RPCs:*` table then
`#### 4.7.2`), spec/04:854 (§4.7.9 step 5), spec/05:453 (`**Scrub model.**`), spec/05:545
(`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**`), spec/06:150-156 (per-slot
fence, `either concurrency` block) and :158 (`**`reserved` hold semantics.**`), spec/06:234
(mid-resume cancel clause), spec/07:23 (atomicity parenthetical and the fenced-listing
insertion point at :22-24), spec/07:210/213/214 (§7.2 preamble, step 2, step 3), spec/07:414
(§7.3 list tail), spec/15:1469/1471 (SDK-warm demotion then `#### 15.4.1`), spec/29:704-711
(§29.4 step 13). All quoted strings are unique. EVIDENCE: spec/04_system-components.md:157,686,854

FACT: All seven bind-sequence RPCs exist on the §4.7 Gateway → Adapter table and all are unary
except `PrepareWorkspace`, which is client-streaming. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151

FACT: All seven bind-sequence RPCs really do create-or-resolve the registry entry, `RunSetup`
included: it calls `ensureSlotPaths` → `ensureSlotStateLocked`, which creates the entry on
first reference. So §4.7.1's "each reports the entry's current epoch" is implementable for all
seven. EVIDENCE: pkg/adapter/staging.go:337; pkg/adapter/slot.go:105-146

FACT: The gateway dials a fresh adapter client per bind (`Binder.DialAdapter` → `adapterclient.Dial`
→ `grpc.NewClient`), so one connection carries one attempt in the shipped tree. That is what makes
§4.7.1's connection-scoped caller latch work; the spec never states the one-session-per-connection
premise it rests on. EVIDENCE: cmd/lenny-gateway/stores.go:2095-2096; pkg/gateway/podlifecycle/podsession/slotbinder.go:237,459
UNVERIFIED: whether a caller that multiplexes two sessions on one adapter connection would name the
wrong session's epoch on a compensation. Not filed (the shipped caller cannot reach it); a later
lens owning the caller contract should decide whether §4.7.1 must state the premise.

WATCHOUT: "open decision D1" and "Option B" appear only in spec-changes.md:190 and :246 and
nowhere else in the proposal. The summary's open-decision section carries entry 11 alone
(summary.md:323-336); entry 9 was deleted by the amendment. Do not assume a D1 exists.

WATCHOUT: the started-session refusal the new edge cases lean on
(spec-changes.md:176-177, :226-227) lives only in code, `codes.Unavailable` "session %s has
already started on this pod". It is in no spec section. Any staged sentence claiming the reclaim
hold is the adapter's only bind-path refusal is therefore false against both the code and the
proposal's own residue argument. EVIDENCE: pkg/adapter/slotsession.go:82-85,70-72,75-78

MISTAKE: the round-5 fix that re-based the epoch on the registry entry rewrote the edge-case
bullet on who meets the reclaim hold ("The retry the §5.2 policy places does not meet the hold",
spec-changes.md:191-194) but left the staged §7.1 paragraph and the Design narrative asserting the
opposite ("spends one of the attempts the retry policy allows", :389 and :60-62). Same class as
the earlier "two rule-stating sites still give the retry-exclusion trigger as an unacknowledged
reclaim" finding: a fix round changes the narrative and leaves the staged normative text behind.
When judging a fix here, grep the staged blocks for the claim, not only the narrative.


### [spec.6.review-citations.1]

FACT: Every file:line citation in spec-changes.md verifies clean. There are exactly four
(`schemas/lenny-adapter.proto:1630-1635`, `spec/04:151`, `spec/04:157`, `spec/29:586-588`) and all
four say what the proposal claims. EVIDENCE: proposals/0081.../0081....spec-changes.md:289,300,305,351
FACT: All sixteen "text to replace" anchors still match spec/ byte for byte and are unique, except
the §29.4 one, which now occurs THREE times in spec/29 (was two when the standing context was
written); the instruction's "In §29.4's numbered step 13" scoping is what resolves it.
EVIDENCE: spec/29_communication-scenarios.md:711
FACT: Every markdown anchor the staged text mints resolves. Newly checked this round:
`#74-upload-safety` (spec/07:438), `#101-horizontal-scaling` (spec/10:3),
`#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#1543-runtime-integration-levels` (spec/15:1707).
FACT: The seven "bind-sequence RPCs" §4.7.1 names all have UNARY responses in the proto, including
the client-streaming `PrepareWorkspace`, so "reports the entry's current epoch on its response"
is wire-implementable for all seven. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151
FACT: All three workspace-prep RPCs really do create/resolve the registry entry, via
`ensureSlotPaths` → `ensureSlotStateLocked`. EVIDENCE: pkg/adapter/slot.go:140-148; staging.go:31,159,317
FACT: The gateway genuinely splits the §4.7.9 step-5 bind sequence across TWO adapter connections:
`Binder.Prepare` and `Binder.Launch` each call `b.reconnect` → `dialSandbox` → `DialAdapter`, which
opens a client per call. The §4.7.1 two-connection caller sentence is therefore true of the tree.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:844,977,1146-1156
FACT: The gateway's §7.4 mid-session upload really does call `PrepareWorkspace` then
`FinalizeWorkspace` on the live binding. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:126,132
FACT: `DemoteSDK` really removes the registry entry (`releaseSessionSlot` after
`anyRegisteredSession`), so §4.7.1's caller rule naming it as an entry-removing RPC is accurate.
EVIDENCE: pkg/adapter/sdkwarm.go:295-298

WATCHOUT: the adapter has bind-path refusals the staged §5.2 hold paragraph does not know about.
`claimSessionSlotUnderLock` refuses a started session with `codes.Unavailable` ("session %s has
already started on this pod") and refuses an SDK-warm claim onto a co-tenanted pod with
`codes.Unavailable` ("pod is not idle"). Any staged sentence of the form "this is the only rule
under which the adapter refuses a request on the bind path" is false against the tree, and the
proposal's own create-time-reserved edge case depends on the started-session refusal surviving.
EVIDENCE: pkg/adapter/slotsession.go:67-73,:80-86; spec-changes.md:176-178

MISTAKE: three rounds of fixes have left `superseded` stated with two incompatible predicates.
§4.7.1 and §15.4 key it on THE ENTRY ("holds an entry for the session at a different epoch"), while
the staged §7.1 paragraph and the Design narrative still key it on THE ATTEMPT ("a reclaim naming an
attempt that no longer holds the slot", "a different attempt owns the identifier"). The two diverge
exactly on the shared-entry case the proposal itself accepts as a residue, where a successor adopts
the surviving entry, the epoch compares EQUAL, and the answer is `reclaimed`. Anyone re-reading the
epoch text should sweep for attempt-vocabulary rather than trusting that the rename finished.
EVIDENCE: spec-changes.md:121,389 versus :679,707 and :182-189

DEFERRED [.non-spec-changes.md]: if the fixer removes the §7.1 clause "spends one of the attempts
the retry policy allows" and the §5.2 "only rule ... on the bind path" sentence, any mirroring
sentence in the non-spec lane's design narrative goes with them.


### [spec.6.review-client-surface.1]

FACT: The r5→r6 delta is a whole redesign of the epoch, not a fix. The epoch is now minted PER REGISTRY ENTRY again (not per attempt), NO bind-sequence request carries one, only `ShutdownRequest` does, and the seven bind-sequence RESPONSES report it. Every standing-context entry written against the "per admitted bind ATTEMPT plus caller echo on seven requests" form is now stale, including the DECISION at Settled and the Deferred on the missing request-side wiring. EVIDENCE: spec-changes.md:105-117,:679; non-spec-changes.md:1051-1110.

CORRECTS [standing context, Settled "MISTAKE: SCHEMA-1 asserts `AssignCredentialsResponse` is an empty message today"]: that entry is WRONG. `schemas/lenny-adapter.proto:1033` is literally `message AssignCredentialsResponse {}`. The fields 1/2/3/5-with-4-reserved the entry lists belong to `RotateCredentialsRequest` (:1035-1053) and `AssignCredentialsRequest`, not to the response. SCHEMA-1's `bind_epoch = 1` on `AssignCredentialsResponse` is correct and the "free number is 6" advice would introduce a gap for no reason. The Open item "AssignCredentialsResponse.bind_epoch = 1 is a hard field-number collision" should be closed as refuted. EVIDENCE: schemas/lenny-adapter.proto:1033.

FACT: No SDK mirrors the adapter proto. `grep -rln "ShutdownRequest\|lenny.adapter" sdks/` hits only comment prose about `/run/lenny/adapter-manifest.json`. `ShutdownRequest`/`ShutdownResponse`/`exited_cleanly` occur in `spec/`+`docs/`+`schemas/*.json` at exactly ONE site, spec/04:157, which SPEC-1 already edits. The seven bind-sequence response messages are named nowhere in spec/ or docs/ except an unrelated `AssignCredentialsResponse.leaseToken` aside at spec/04:1538 and the retired-name block at docs/api/internal.md:79,:123. So SCHEMA-1's nine fields and one enum have no unstaged parallel representation outside `docs/reference/adapter-contract.md`, which the standing Deferred already owns and which this loop may not edit.

FACT: `examples/runtimes/echo/` DOES NOT EXIST in the tree (it is a Phase-2 deliverable), so the standing Open "Is `examples/runtimes/echo/` an adapter rather than a runtime binary?" cannot be answered from the tree and cannot be an unstaged site. spec/15:1491 and :1812 both describe it as a RUNTIME writing JSONL to os.Stdout, so :1467's "reference implementation of the adapter" is a pre-existing mislabel unrelated to 0081.

FACT: All seven bind-sequence RPCs return a single unary response (`PrepareWorkspace` is client-streaming in, unary out; none is server-streaming), so §4.7.1's "reports the entry's current epoch on its response" is well defined for each. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151.

FACT: §7.4's mid-session upload really does reuse `PrepareWorkspace`+`FinalizeWorkspace`, and it sends them on `bind.Adapter`, the LIVE binding connection from `podRegistry`, not on a bind attempt's connection. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:110-137.

FILED (1): §4.7.1's caller rule "A caller names the epoch it holds on a `Shutdown` for that session and names no other" makes the ordinary session-end `Shutdown` and the §11.4 revoke fenced, because both ride `BindResult.Adapter`, the same connection whose bind-sequence responses latched the epoch. The same paragraph and the §4.7 row both assert the epochless form "is what every caller other than a fenced compensation sends", and CODE-6 has plain `Shutdown` pass a zero epoch. EVIDENCE: spec-changes.md:681,:321,:324-327; non-spec-changes.md:996-998,:1013-1015; pkg/gateway/podlifecycle/podsession/slotbinder.go:542.

FILED (2): §5.2's staged hold paragraph closes "This is the only rule under which the adapter refuses a request on the bind path", which is false against the shipped adapter (`claimSessionSlotUnderLock` refuses a repeat start with `codes.Unavailable` at slotsession.go:82-84 and an SDK-warm non-idle pod at :68-71) and against the proposal's own edge cases, which lean on that started-session refusal twice. EVIDENCE: spec-changes.md:601,:176-177,:226-227; pkg/adapter/slotsession.go:66-85.

WATCHOUT: `diff -rq` against `scratchpad/cp-snap/.../spec-r6` is EMPTY again. The real r6 delta is against `spec-r5`, and it is large (the whole epoch redesign). Do not conclude "nothing changed".

UNVERIFIED: §15.4's hold block adds "`Shutdown` is not held" while §5.2's hold paragraph has no such carve-out. I judged this NOT a contradiction, because `Shutdown` neither creates nor resolves an entry once the entry is deregistered, so §5.2's predicate does not reach it and §15.4's sentence is a clarification. A later lens tempted by it should start from that reading.

UNVERIFIED: §5.2's hold paragraph asserts the hold's bound "also covers the slot cleanup's own removal of the slot's directories". Nothing bounds `removeSlotTree`: it runs after the close, outside `s.mu`, and the `Shutdown` handler performs no context-expiry check (standing Settled). I did not file it because it is a reliability/mechanism call rather than a client-surface one and a close variant was refuted last round on the old wording, but the wording is NEW this round and the old refutation does not cover it.


### [spec.6.review-docs-alignment.1]

FACT: the r5→r6 fix round REDESIGNED the epoch from per-bind-ATTEMPT (with a request-side
echo and an admission rule) back to per-registry-ENTRY with NO bind-sequence request
carrying an epoch. `diff -u scratchpad/cp-snap/.../spec-r5/*.spec-changes.md` against live
is the only way to see it; `spec-r6` is byte-identical to live and diffing it shows nothing.
EVIDENCE: spec-changes.md:106-118 versus spec-r5 same block.

FACT: the redesign created THREE new edge-case rows that did not exist at r5, and my own
lens returned empty at r5 on the pre-redesign text. New rows: "A compensation still on the
wire when a retry adopts the surviving entry is not fenced" (spec-changes.md:232-246), "An
abandoned attempt's start whose claim runs after the reclaim completed re-creates the entry"
(:247-258), and a rewritten "A start that races the reclaim" whose r5 form asserted "three
orderings arise and none of them reports a ..." and whose r6 form accepts an orphan
(:219-224). A round that trusts `spec.5.review-docs-alignment.1`'s empty verdict is trusting
a verdict on text that no longer exists.

WATCHOUT: "open decision D1's Option B" is cited twice (spec-changes.md:190,:246) and there
is no decision D1 anywhere in the proposal or in either review log; the summary's open
decisions section holds entry 11 alone and records entry 9 as deleted-because-resolved
(summary.md:323-333). The fixer that redesigned the epoch invented the reference for the
"discriminator on every entry-creating request" option it dropped. Filed as a citation
finding.

DECISION: filed three findings, all against the Edge cases section — BECAUSE the redesign's
two new accepted outcomes (a completed `reclaimed` reclaim tearing down a session already
answered to its client; a completed reclaim leaving the pod holding an entry and a runtime
session no gateway attempt owns) land in NO staged spec sentence, while staged §7.1 presents
a closed disposition taxonomy in which only a reclaim that "did not complete" leaves residue
(spec-changes.md:378 block) — ALTERNATIVES: I dropped the `lenny_adapter_leaked_slots` angle
(SPEC-3 names a gauge absent from spec/16 and docs/reference/metrics.md, but spec/05:545 and
spec/06:160 already name it, so the omission is pre-existing) and every docs/-remedy finding,
which this loop may not close.

USEFUL [Standing context, "No alert anywhere references any slot metric" / "spec/16 needs no
edit and can orphan no alert"]: killed the whole alert/runbook half of this lens in one read.


### [spec.6.review-edit-sites.1]

FACT: `diff -rq` between the r6 snapshot and the proposal directory is EMPTY — nothing changed
since the previous round, so the "read the changed sections first" instruction had no target this
round and the whole staging had to be re-read cold.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r6

FACT: the adapter already refuses bind-path RPCs on two shipped conditions, and both return
`codes.Unavailable`: "pod is not idle: session %s is already bound on this pod" and "session %s has
already started on this pod".
EVIDENCE: pkg/adapter/slotsession.go:71, pkg/adapter/slotsession.go:85

FACT: §10.1 hold state is a third bind-path refusal rule — "All other inbound RPCs are rejected with
`UNAVAILABLE` status and a `coordinator_hold` error detail".
EVIDENCE: spec/10_gateway-internals.md:57

WATCHOUT: SPEC-3's reclaim-hold paragraph closes with an absolute exclusivity claim ("This is the
only rule under which the adapter refuses a request on the bind path"), mirrored in the Design
narrative. Three independent sources falsify it, including the proposal's own edge-case bullet.
EVIDENCE: spec-changes.md:601, spec-changes.md:148, spec-changes.md:177

FACT: none of the seven bind-sequence request messages carries `coordination_generation`, so
spec/10:30's "Pods validate the generation on every gateway→pod RPC" is already over-broad against
the proto. Do NOT use that line as the evidence for a bind-path refusal; spec/10:57 is the sound one.
EVIDENCE: schemas/lenny-adapter.proto (PrepareWorkspaceRequest … ResumeRequest field lists);
spec/10_gateway-internals.md:30

FACT: `ShutdownRequest` field numbers 1,2,3,5,6 are taken and 4 is reserved (`slot_id`), so field 7
is the first free number for `expected_bind_epoch`.
EVIDENCE: schemas/lenny-adapter.proto:1609-1636

FACT: `Shutdown` appears in no §28.3 register row and §28.5.1 carries only CH-ATTACH, CH-CHECKPOINT,
CH-FENCE, CH-BARRIER, CH-PODHEALTH and CH-ADAPTEREVENTS. §29.2 states explicitly that the
workspace and session-start RPCs of steps 15, 16, 17, 19, 22 and 23 have no register entry. The
"§28's registers untouched" claim holds.
EVIDENCE: spec/28_communication-channels.md:106,118-123; spec/29_communication-scenarios.md:233-235

FACT: `/run/lenny/slots/{sessionId}/credentials.json` is the credential path everywhere in spec/,
so SPEC-3's widened §5.2 action list cites it correctly.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461; spec/04_system-components.md:914

FACT: the §7.2 anchors are exact — preamble premise at spec/07:210, step 2 at :212, step 3 at :213 —
and §29.4 step 13 ends with the quoted `([§15.4.3](...), §28.5.3).` at spec/29:709. §5.2's
`**Max retries:**` sentence is at spec/05:555 and the `**Scrub model.**` paragraph at :453.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md]: `docs/reference/adapter-contract.md:75`
is a docs mirror of the §4.7 `Shutdown` row and it states the teardown as ONE act with ONE
precondition: "The adapter flushes the session's final usage report, closes its runtime, removes its
slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`." SPEC-1 splits that
into a slot release (runs whenever an entry exists) and a runtime teardown (runs only for an admitted
start), SPEC-3 withholds the `ReportSessionScrub` outcome on the pre-`running` path, and SPEC-5 adds
the bind-epoch precondition and the three reclaim outcomes. The row is therefore false after the spec
lands. It is in NO edit list: the only docs deliverable is DOCS-1 for
`docs/reference/state-machines.md` (non-spec-changes.md:1147, :1836). The non-spec lane needs a
DOCS-2 for `docs/reference/adapter-contract.md:75`, and should re-check :81 (`ReportSessionScrub`
"at each session release") and :84 against SPEC-3's withheld-report rule while it is there.

UNVERIFIED: SPEC-5's §4.7.1 states that a bind attempt's stages may span TWO connections to one pod,
while §28.3's `LNK-POD-GRPC` row states "One connection per gateway replica per pod"
(spec/28:106). Production dials a fresh adapter client per connect
(`pkg/gateway/podlifecycle/podsession/binder.go:1152,:1776` via `DialAdapter`), so the code sides with
the proposal. I did not file this: §29.2 places the bind RPCs outside the §28 register, so the
`LNK-POD-GRPC` multiplicity arguably does not govern them. Somebody with the channel-naming lens
should settle whether §28.3's multiplicity column reaches unregistered gateway→adapter RPCs.

OPEN: SPEC-3's hold says "the adapter admits no request that would create or resolve a registry entry
under it", while SPEC-5's §15.4 hold block carves out `Shutdown` ("`Shutdown` is not held"). I judged
this reconcilable — during the hold no entry exists, so the "resolve" arm is vacuous and the §15.4
sentence is a clarification rather than an exception — and did not file it. If a later round decides
the two are genuinely incompatible, the fix is one clause in the §5.2 paragraph, not in §15.4.


### [spec.6.review-feasibility.1]

FACT: The r6 delta is large and reverses the r5 design. The epoch is now minted PER REGISTRY ENTRY again, no bind-sequence request carries one, and the hold is the only refusal the design intends on the bind path. The shared-entry ordering the earlier round killed the per-entry form over is now an ACCEPTED residue (two new edge bullets). Diff `spec-r5` (not `spec-r6`, which is byte-identical to the live proposal). EVIDENCE: proposals/0081_.../0081_....spec-changes.md:105-117,:182-197,:232-246

FACT: `DialAdapter` is a fresh dial per slot bind (`connectSlot`, pkg/gateway/podlifecycle/podsession/slotbinder.go:456-460), so the per-connection epoch latch §4.7.1's caller rules assume is per session in practice. A co-tenanted pod does NOT share one gateway→adapter connection. I chased "the latch could name a co-tenant's epoch" and it dies here. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:415-476

FACT: exactly seven call sites create/resolve an adapter registry entry, and they are exactly the seven bind-sequence RPCs §4.7.1 names: staging.go:134 (PrepareWorkspace), :181 (FinalizeWorkspace), :337 (RunSetup), slotcreds.go:26 (AssignCredentials), slotsession.go:75 (claimSessionSlotUnderLock ← StartSession/Resume/ConfigureWorkspace). No eighth. EVIDENCE: grep ensureSlotStateLocked/ensureSlotPaths over pkg/adapter/*.go

FACT: the §5.2 hold's "a hold taken outside any request" resolves to a real pass: `deregisterStartedSessions` (pkg/adapter/slotsession.go:375-395), the §10.1.4/§6.1 termination sweep. And "a release that runs its cleanup inside the RPC that requested it" is true of `DemoteSDK`, which calls `releaseSessionSlot` before answering (pkg/adapter/sdkwarm.go:294-298). Both clauses check out; do not re-derive.

FACT: §7.4's mid-session upload really does reuse the adapter's `PrepareWorkspace` and `FinalizeWorkspace` on the session's live binding, so the staged "needs no rule of its own" is grounded. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:126-138; pkg/adapter/staging.go:240-268

WATCHOUT: the staged §5.2 hold paragraph says "This is the only rule under which the adapter refuses a request on the bind path" while the SAME proposal's edge case relies on the started-session refusal and §4.7.1's own epoch/generation sentence says the generation "is validated on the RPCs that carry it" (every bind-sequence request carries `coordination_generation`). Filed. EVIDENCE: spec-changes.md:601,:177,:677; spec/10_gateway-internals.md:30; pkg/adapter/slotsession.go:67-84

WATCHOUT: the §5.2 hold's bound clause ("In each case the bound also covers the slot cleanup's own removal of the slot's directories") contradicts the edge case two hundred lines above it ("plus the removal of the slot's directories") and the non-spec lane's own statement that `removeSlotTree` is "an uninterruptible `RemoveAll`". `removeSlotTree` takes no context at all (pkg/adapter/slot.go:210-212). Filed. EVIDENCE: spec-changes.md:601,:199; non-spec-changes.md:128-131

MISTAKE: the r6 rewrite introduced two references to "open decision D1's Option B" (spec-changes.md:190,:246). No decision D1 and no "Option B" exists anywhere in the proposal; the only open decision is entry 11, and summary.md:326-328 states the opposite conclusion, that entry 9's create-time-reserved exposure "is resolved ... the bind epoch and the reclaim hold close it". Filed.

UNVERIFIED: §15.4 now pins the hold refusal to `ABORTED` "which is the transient classification a caller retries on", and names a client-driven §7.3 resume among the callers that can meet the hold. `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3681) matches neither `Aborted` nor a `*SlotBindError`, so on the resume path an ABORTED hold refusal is not classified transient. Not filed: the remedy is gateway code, and the material skeptic already refuted a close variant of the status-code family. A code-lane reviewer should decide.

UNVERIFIED: the §5.2/§15.4 hold refusal predicate is "a request that would create or resolve a registry entry", which literally reaches `Attach`, `Interrupt`, `Checkpoint`, `ReportUsage` and every other entry-resolving RPC, while CODE-6 puts the refusal only in `ensureSlotStateLocked` (the seven bind-sequence RPCs). Judged thin (during a hold the entry is gone, so those fail anyway and only the status code differs) and not filed. If the predicate is ever widened or narrowed, decide this at the same time.


### [spec.6.review-fresh.1]

FACT: The r6 delta is a WHOLESALE REVERT of the epoch design, from per-admitted-attempt back
to PER REGISTRY ENTRY, with no request-side epoch at all. Diff `spec-r5` against the live
proposal (`spec-r6` and `spec-r6-start` are byte-identical to it). Consequences a later agent
must hold: no bind-sequence request carries an epoch; the only refusal on the bind path the
staged text names is the reclaim hold; the §7.4 mid-session-upload problem and the
seven-request-field DEFERRED are both dissolved by that revert.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:105-117,:677-681

WATCHOUT: the standing context still records "MISTAKE that cost a whole design round: minting
the bind epoch PER REGISTRY ENTRY" (review-log.md:505) and the orchestrator's already-fixed
list opens with the same finding. r6 deliberately went BACK to it and recorded the resulting
hole as two accepted failure modes (spec-changes.md:182-190, :232-246). Do not re-file the
per-entry inheritance hole as a fresh finding; it is now a recorded residue. Do file the
places where the surrounding prose still claims the fence closes it.

FACT: under the per-entry epoch the headline race is NOT fenced. A retry that reaches the pod
before the lagging reclaim's critical section RESOLVES the abandoned attempt's surviving entry
and inherits its epoch, so the reclaim compares EQUAL and tears the retried session down. The
proposal says so itself at spec-changes.md:182-190 and :232-246. The epoch is load-bearing
only where the old entry was actually released before the new one was created (for example the
two-connection prepare/launch split, where a second compensation can arrive after a successor
entry exists).

FACT: `Prepare` and `Launch` each dial their own adapter connection
(`pkg/gateway/podlifecycle/podsession/binder.go:844`, `:977`, both via `reconnect` →
`dialSandbox` at `:1146`), and `materializeSlot` dials once (`slotbinder.go:459`). So the
per-connection epoch latch never multiplexes two sessions, and §4.7.1's two-connection
sentence is grounded.

FACT: the adapter has THREE bind-path refusals that are not the reclaim hold:
`codes.Unavailable "session %s has already started on this pod"`, `codes.Unavailable "pod is
not idle"`, and the `codes.InvalidArgument` resolve wrap.
EVIDENCE: pkg/adapter/slotsession.go:69-71,:76-78,:82-84

FACT: `removeSlotTree` is `slotlayout.RemoveTree(st.paths)` with no context at all
(pkg/adapter/slot.go:210-212) and runs AFTER the grace-bounded `Runtime.Close` inside the
`Shutdown` handler (pkg/adapter/session.go:262-271). Any spec sentence claiming a bound
"also covers" the directory removal has to answer that.

FACT: mid-session upload really does reuse `PrepareWorkspace` + `FinalizeWorkspace` on the
live binding (`pkg/gateway/sessionserver/upload_to_session.go:128,:134`), and
`PrepareWorkspace` resolves the staging dir exactly once per call (`stagingDir == ""` guard,
pkg/adapter/staging.go:76-84). Both §4.7.1 claims about §7.4 and client streaming check out.

FACT: `DemoteSDK` removes the entry AND the tree synchronously inside the RPC
(`pkg/adapter/sdkwarm.go:296-299` → `releaseSessionSlot`, slotsession.go:214-220), so §5.2's
"a release that runs its cleanup inside the RPC that requested it ends the hold before that
RPC answers" holds for the demotion case.

OPEN: does the staged hold rule reach the FORWARD RPCs? §5.2:601 and §15.4:711 say the adapter
refuses "a request that would create or resolve a registry entry" while the identifier is
held, with `ABORTED` and with "refuses one with a permanent status ... does not conform". On
the plain "would" reading that covers `Attach`, `Interrupt`, `Checkpoint`, `ReportUsage` and
the credential RPCs, which today answer `FailedPrecondition` when no entry exists, and CODE-6
stages the refusal only inside `ensureSlotStateLocked` (non-spec-changes.md:955-962). Not
filed: during a hold there is no entry to resolve, so the narrow reading is available. A
later round should settle whether the phrase wants scoping to the bind sequence.

UNVERIFIED: `ABORTED` now appears in the staged §15.4 and in no other file under `spec/`,
`docs/` or `schemas/` (`grep -rn ABORTED` returns nothing). Nobody has checked whether the
five `codes.InvalidArgument` resolve wraps the non-spec lane routes through `slotResolveError`
actually preserve it end to end; that is the code loop's item.

DEFERRED [proposals/0081_.../0081_....summary.md, the open-decisions preamble at :325-329]: it
says "Entry 9, the create-time-reserved retry's unbounded exposure, is resolved and has left
this section: the bind epoch and the reclaim hold close it." After the r6 revert to a
per-entry epoch that is false: spec-changes.md:182-190 states the same path now tears down a
running retried session and calls it an accepted failure mode. The summary also carries no
"open decision D1", which the spec staging cites twice.


### [spec.6.review-kubernetes.1]

DECISION: filed ONE finding (the dangling `open decision D1` reference) and returned nothing under the Kubernetes lens proper — BECAUSE the round-6 rewrite moved the design FURTHER from any Kubernetes surface, not toward one: the epoch is now per registry entry, in-memory, reported on responses only, and the hold is an in-memory identifier set; `grep -in "controller|RBAC|finalizer|admission|status|CRD|informer|watch|etcd|reconcil|SandboxClaim|annotation"` over `.spec-changes.md` still returns only non-actor hits (`status` at :506 is "authoritative-enumeration status", `CRD validation rule` at :619 is a no-change note, `admission`/`admits` is the ADAPTER admitting an RPC). ALTERNATIVES rejected: the edge-triggered-reclaim-with-no-level-triggered-backstop dress (already refuted at Standing context; §4.6.1 orphan GC is the backstop and the two NEW residues the round adds are recorded as accepted failure modes); the undefined "termination window of the pass that runs the cleanup" bound (see WATCHOUT below).

CORRECTS [Standing context Settled "DECISION: the bind epoch is minted per admitted bind ATTEMPT, not per registry entry"]: as of the r5→r6 fix pass the proposal REVERTED to per-ENTRY minting and DELETED the request-side echo entirely. spec-changes.md:105-117 now reads "The epoch names the registry entry the adapter holds for a slot identifier ... No bind-sequence request carries an epoch and none is refused on one", :677-679 states the epoch is "validated on `Shutdown` alone", and the two admission rules are gone: the hold is now the only rule that refuses a request on the bind path (:601). The inheritance hazard the per-attempt form existed to close is now ACCEPTED, as two new edge-case bullets (:232-246, :247-258). Everything in Standing context that assumes a request echo is stale: entries #308, #503, #504, the request-side DEFERRED at review-log.md:2491, the §7.4 mid-session DEFERRED, and the five-step checklist DEFERRED at :625 (S8's "nine fields" is now correct again, sixteen is not). No review-log shard documents this pass; the change is visible only in `diff spec-r5 <live>`.

FACT: the r6 delta is real and large, and `spec-r6`/`spec-r6-start` are byte-identical to the live proposal as usual. Diff against `scratchpad/cp-snap/0081_.../spec-r5`. The hunks are: the Design epoch paragraphs, the §4.7 `Shutdown` row's outcome enumeration replaced by a pointer to §4.7.1, three edge-case bullets rewritten and two added, the §7.1 "names the registry entry" clause, the §5.2 reclaim-hold paragraph rewritten and re-bounded, the §6.2 prose's §4.7.1 citation dropped, and both SPEC-5 blocks rewritten (§15.4 now names `ABORTED` explicitly).

FACT: the ordinary session-end `Shutdown` DOES ride the bind connection, so it carries the latched epoch. `Binder.ReleaseSlot` sends `result.Adapter.Shutdown(...)` on `BindResult.Adapter`, which is the client the bind dialled. The standing Trap saying "every other `Shutdown` sender is on a connection that latched nothing" is wrong for session end; it is harmless, because the entry the bind created is the entry the session holds, so the epoch matches, and §4.7.1's "holds none once ... a `Shutdown` answering `reclaimed` removes it" is what keeps the occupancy-zero recycle `ShutdownRecycle` epochless. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:528-545.

WATCHOUT: the new §5.2 hold paragraph bounds a hold "taken outside any request" by "the termination window of the pass that runs the cleanup" (spec-changes.md:601). `grep -rn "termination window" spec/` returns ZERO hits, so that bound names a quantity the specification does not define, on a fail-closed gate. I declined it because the operative rule ("until the cleanup that reclaims the slot has finished") is well defined and the clause is descriptive, and because the default-to-refuted rule bites. A round that wants it should argue from §15.4, which points at §5.2 for the whole hold and therefore inherits the undefined bound into the third-party contract.

WATCHOUT: do NOT re-file the §7.1 "spends one of the attempts the retry policy allows" clause against the new edge-case sentence "The retry the §5.2 policy places does not meet the hold" (spec-changes.md:344 versus :192-194). The pair now reads more contradictory than it did, but `spec.5.review-applicability.1` already rejected the identical sentence pair as consequence-then-mitigation, and the reading under which §7.1's "further attempt" means a non-policy attempt survives.

WATCHOUT: the §4.7 `Shutdown` row no longer enumerates the three outcomes; it points at §4.7.1 for them (spec-changes.md:321). SPEC-1 lands that row at S1 and SPEC-5 lands §4.7.1 at S5, so between S1 and S5 the applied spec names outcomes stated nowhere. This is the SAME item as Standing context Open "S1 lands spec text that cites a §4.7.1 statement S5 lands later", now one step worse, and its remedy is still a checklist reorder the spec loop may not make. Do not spend a finding on it; do make sure the between-loops pass reorders SPEC-5 ahead of SPEC-1 or restores the enumeration.

OPEN: the summary now cites "the §10.1.4 hold termination" as a third site that takes the reclaim hold (summary.md, the hold bullet). §10.1.4 exists (spec/10_gateway-internals.md:53, "Coordinator-Loss Detection and Hold State") but Standing context #164 records that the coordinator hold cannot arm in production, so the third hold site may be unreachable and its "termination window" bound may be vacuous. A non-spec-lane reviewer should decide whether the helper needs that caller at all.


### [spec.6.review-mechanism.1]

FACT: Round 6 REVERSED the epoch design back to PER-REGISTRY-ENTRY minting and deleted the
request-side echo entirely. Compaction pass 7's standing context and its "already fixed" list
both record the OPPOSITE (per admitted ATTEMPT + seven request fields), so the standing context
is now stale on its single most load-bearing entry. Read `spec-changes.md:106-127` and `:679`
before trusting any epoch entry in `## Standing context`.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:106-113,:679; diff against
scratchpad/cp-snap/0081_.../spec-r5 (spec-r6 and spec-r6-start are byte-identical to live).

FACT: under the reverted design the epoch fences ONLY the ordering in which the failed attempt's
entry was already released and a NEW entry was created. The primary residue class (a gateway-side
failure leaving the attempt's entry alive, which a retry then adopts) compares EQUAL and is now
recorded as an accepted failure mode. The proposal points that residue at "open decision D1's
Option B".
EVIDENCE: spec-changes.md:181-190,:231-246.

FACT: the adapter's own pre-start failure branches (`releaseSessionSlot` at session.go:133,:147,
:157; resume.go) DO remove the entry, so `superseded` is reachable in production rather than
theoretical. That is the half the epoch closes.
EVIDENCE: pkg/adapter/session.go:133,:147,:157.

WATCHOUT: `summary.md`'s `## Open decisions for human to make` contains ONLY entry 11. There is no
"D1" and no "Option A/B" anywhere in the proposal outside the two spec-changes citations. The same
section still says entry 9 "is resolved ... the bind epoch and the reclaim hold close it", which the
round-6 edge cases now contradict outright. FILED as a citation finding on spec-changes; the summary
half is out of this loop's scope (how an open decision is framed is not filable).
EVIDENCE: summary.md:323-337; spec-changes.md:190,:246.

FACT: the mid-session upload really does reuse the adapter `PrepareWorkspace` and
`FinalizeWorkspace` on the binding the session already holds, so §4.7.1's §7.4 sentence is
accurate. Note §7.4 itself never names those RPCs; the ground is the handler.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:66-70,:123-127.

FACT: `PrepareWorkspace` resolves the staging dir exactly once, from the first frame's session id
(`stagingDir == ""` guard), so §4.7.1's client-streaming sentence and §15.4's matching
non-conformance clause are true against the tree.
EVIDENCE: pkg/adapter/staging.go:75-82,:132-142.

UNVERIFIED: §5.2's new hold-bound sentence ("bounded ... by the graceful window the reclaiming
request carries ... by that request's own deadline when it carries none ... In each case the bound
also covers the slot cleanup's own removal of the slot's directories"). The `Shutdown` handler
performs no context-expiry check and `removeSlotTree` runs after the close with no deadline, and
CODE-6 releases the hold on a plain `defer`, so no component enforces either stated bound. Also
"graceful window" and the `Shutdown` `deadline_ms` field appear nowhere in `spec/`. NOT filed: a
close variant ("the hold is given a bound its own cited source does not provide") was refuted on
materiality one round ago. A reliability or code-lane reviewer should settle whether the bound is
an obligation CODE-6 must implement.
EVIDENCE: spec-changes.md:601; pkg/adapter/session.go (Shutdown handler, no ctx.Err check);
non-spec-changes.md:892-923.

UNVERIFIED: §5.2 and §15.4 now scope the hold's refusal to "any request that would create or
resolve a registry entry", which is much wider than the bind sequence (Attach, Interrupt,
SendMessage, Checkpoint, ExportPaths, ReportUsage, the three credential RPCs all resolve the
entry). §6.2's prose and the Design paragraph still say "a bind". Judged an abbreviated gloss
rather than drift, because during the hold the entry is already deregistered so those RPCs
resolve nothing. Somebody may want the wide predicate narrowed to the bind sequence.
EVIDENCE: spec-changes.md:601,:659,:143-146,:711.

MISTAKE: the round-6 fixer rewrote three sites to entry vocabulary (§4.7 row, §7.1's "On
superseded" clause, §4.7.1) and left §7.1's `superseded` TRIGGER sentence in attempt vocabulary,
and rewrote the "retry meets the hold" edge case to say the policy-placed retry never meets it
while leaving §7.1's staged text asserting that it does and spends a policy attempt. Both filed.
The pattern is the one the standing context already names: a design reversal that touches many
sentences leaves the ones a grep on the new vocabulary does not hit.

DECISION: filed four findings and no more. BECAUSE the remaining candidates (the wide refusal
predicate, the unenforced hold bound, the per-connection epoch latch not being bound to a session)
each have a refuted close variant in the standing context. ALTERNATIVES: filing the Design
paragraph's "The epoch closes the late reclaim" over-claim at :148, rejected because the Design
section is rationale and the material skeptic refuted an Edge-cases-narrative finding on exactly
that ground last round.


### [spec.6.review-operational.1]

FACT: Round 6 REVERSED the epoch design again. The epoch is now minted PER REGISTRY ENTRY (not per attempt), no bind-sequence request carries one, and the only refusal mechanism on the bind path is the reclaim hold. Standing-context Settled #310 ("minted per admitted bind ATTEMPT ... echoed on the seven bind-sequence REQUESTS") and the whole DEFERRED at review-log.md:626 (seven request-side proto fields) are now STALE against the live staging. Do not fix the proposal toward them. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:676-686 ("none of them carries one on its request"), :601.

FACT: `diff -rq` against every `scratchpad/cp-snap/0081.../spec-r*` snapshot: only `spec-r5` and `spec-r5-prefix` differ from live; `spec-r6`, `spec-r6-start` are byte-identical. The real r5→r6 delta is the epoch re-design plus a full rewrite of §5.2's `**Slot-identifier reclaim hold.**` paragraph (spec-changes.md:601) and four edge-case bullets. Diff against `spec-r5-prefix`.

WATCHOUT: the §5.2 hold paragraph at spec-changes.md:601 is ONE physical line carrying eleven independent normative sentences, three of which this round filed on. Read the whole line before editing any clause; a grep anchored on one sentence misses the neighbours, which is the same disease review-log.md:397 records for §7.1's long line.

FACT: the alert half of this lens is still structurally inert and I re-derived it. `grep -rn "lenny_adapter_leaked_slots" spec/ docs/ pkg/alerting/` returns spec/05:545 and spec/06:160 only; no alert rule and no runbook names any slot metric, and the amendment adds no metric, no condition and no CRD status write. Everything I filed is spec-internal consistency reached through the operational surface rather than an alert/metric gap. A later operational lens can skip the alert sweep entirely.

FACT: `removeSlotTree` is unbounded on BOTH cleanup paths, not just `Shutdown`. `pkg/adapter/session.go:271` runs it after `Runtime.Close` with no ctx and no expiry check, and `pkg/adapter/holdstate.go:253` does the same inside the §10.1.4 termination pass. Any future sentence bounding the hold has to name the close alone, or say the tree removal is unbounded.

UNVERIFIED: §5.2's new hold sentence "a bind refused this way is accounted by the gateway as an ordinary transient slot failure" is true for the create-time-reserved retry (`materializeSlot` → `recordSlotFailure`) and false for the client-driven §7.3 resume, which emits no `lenny_slot_failure_total` at all (standing Settled #173) and reaches accounting only through CODE-5's new `accountSlotFailure` caller. The proposal's own edge case at spec-changes.md:191-196 names that resume as one of the three things that can meet the hold. I did not file it, because "accounted as a transient slot failure" reads as the §5.2 classification rather than the metric, and the classification claim is true. A reliability or test-coverage lens with the resume path open should settle which reading the sentence means.

OPEN: two accepted-failure-mode bullets defer the worst residue (a serving session torn down) to "open decision D1's Option B", and no decision D1, and no Option A/B, exists anywhere in the proposal. Filed as a false citation. Whoever fixes it must decide whether a decision was meant to be added to summary.md's `## Open decisions for human to make` (which carries entry 11 alone) or whether the bullets should name the mechanism directly.

DECISION: I did NOT file §7.1's "a further attempt ... meets that hold ... and is refused as a transient condition that spends one of the attempts the retry policy allows" (spec-changes.md:388) against the edge case's "The retry the §5.2 policy places does not meet the hold" (:191). BECAUSE Settled #315 records the sentence as the RATIONALE for the exclusion ("which is the cost the exclusion avoids"), and the semicolon clause that follows states the exclusion, so the pair reads as counterfactual-then-rule. ALTERNATIVES: filing it as a contradiction or as an unreachable trigger; both die on that reading and would cost two verifiers.


### [spec.6.review-performance.1]

FACT: The capacity half of this lens is genuinely inert on this delta, and I re-derived it rather than trusting the standing entry. The hold is a per-slot-identifier `map[string]struct{}` under `s.mu`, taken in the deregistration critical section and released after the destructive steps run OUTSIDE the lock, so one cleanup blocks only its own identifier and never the pod's other slots; `slotState.epoch` is 8 bytes per entry; SCHEMA-1 adds scalar varints to seven responses. No etcd write, no Postgres row, no Redis key, no informer, no new metric series. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:880-923.

FACT: The per-slot cleanup timeout `max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)` is NOT enforced anywhere in `pkg/adapter`. `grep -n "cleanupTimeout\|CleanupTimeout" pkg/adapter/*.go` returns only `podscrub.go:128`, which is the WHOLE-POD scrub budget. So SPEC-3's staged sentence "is the deployer's budget for that cleanup and is not a second bound on the hold" is consistent with the tree, even though spec/05:545 says "minimum 5s enforced at runtime by the adapter". Do not file the disclaimer as a contradiction; I checked and it is not one. EVIDENCE: pkg/adapter/podscrub.go:128; spec/05_runtime-registry-and-pool-model.md:545.

FACT: The gateway compensation does carry a bound. `slotCleanupBudget(cleanupTimeoutSeconds, maxConcurrentSessions)` is threaded into `context.WithTimeout(context.WithoutCancel(ctx), budget)`, and `ShutdownRequest.deadline_ms` (field 3, schemas/lenny-adapter.proto:1612) is sent as 0 on that path, so the staged "by that request's own deadline when it carries none" arm is the one that fires for a compensation. EVIDENCE: non-spec-changes.md:506-513,:550; schemas/lenny-adapter.proto:1609-1612.

MISTAKE: The §7.1 staged paragraph was not updated when this round rewrote the reclaim-hold edge case. The old bullet said any retry meeting the hold spends a §5.2 retry; the new bullet says the §5.2-placed retry cannot meet the hold at all (the pod is excluded, or the cleanup finished before the answer). §7.1's sentence still carries the old claim AND the new exclusion clause in one sentence. Filed as my only finding. EVIDENCE: spec-changes.md:389 vs :191-194; the exclusion's discriminator is `sbe.Leaked || relErr != nil` at non-spec-changes.md:816-818.

WATCHOUT: SPEC-3's hold paragraph says the adapter "admits no request that would create or resolve a registry entry under it" while a hold is up. Read literally that reaches `Shutdown`, which resolves an entry, and CODE-6 does not refuse `Shutdown` there (the hold check lives at the top of `ensureSlotStateLocked`, which `Shutdown` does not call). I did NOT file it: the next sentence scopes the rule to the bind path ("This is the only rule under which the adapter refuses a request on the bind path"), and the text predates this round's rewrite so several sweeps have read it. A round that wants to file it needs to show the scoping sentence does not govern. EVIDENCE: spec-changes.md:601.

UNVERIFIED: whether a client-driven §7.3 resume that meets the hold is retried anywhere. The edge-case bullet names it as a caller that can meet the hold and calls the refusal transient, but I did not trace whether the resume path has a retry loop that consumes the transient. If it does not, the transient refusal is terminal for that resume. Somebody on the mechanism lens should settle it. EVIDENCE: spec-changes.md:194-196.


### [spec.6.review-reliability.1]

DECISION: Returned an empty findings list for the reliability lens on the staged spec edits — BECAUSE every recovery mechanism I traced (the epoch fence, the reclaim hold, the leaked disposition, the withheld report, the §7.2/§7.3 reclaim delegation) survived the crash/restart/retry orderings I could construct, and the two candidates I built both died on evidence in the tree — ALTERNATIVES: I considered filing (a) an adapter-process-restart ABA on the epoch and (b) a §5.2-hold versus §15.4-`Shutdown`-exemption contradiction; both are refuted below.

FACT: **The agent pod is `RestartPolicy: Never`, so an adapter process cannot restart in place on a live pod.** This closes the standing Open item "Can an adapter restart produce an epoch collision?". The hazard I built was: the gateway's adapter "connection" is a `grpc.ClientConn` from `grpc.NewClient` (pkg/gateway/runtime/adapterclient/client.go:50), which reconnects transparently, so the earlier justification ("the old connection dies with the process") is false as stated — a ClientConn does NOT die with the adapter process, it goes TransientFailure and recovers (`Alive()` at client.go:72-79 reads exactly that). If a new adapter process could come up behind the same channel, its counter restarts at 1 and a stale compensation naming epoch 1 would compare EQUAL against a retry's first entry and tear down a live session. The premise fails only because the container is never restarted: `pkg/controller/sandbox/podspec/podspec.go:980` sets `RestartPolicy: corev1.RestartPolicyNever`, pinned by `podspec_test.go:94-95`. EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:980; pkg/gateway/runtime/adapterclient/client.go:50,:72-79.

WATCHOUT: Do not re-file the standing Open item's justification as a false-citation finding either. The reason recorded for closing it is wrong, but the conclusion is right, and the correct reason is the pod restart policy above. EVIDENCE: review-log.md Open item "Can an adapter restart produce an epoch collision?".

MISTAKE (nearly filed, do not re-derive): "§5.2's hold rule refuses every request that would create or resolve an entry, which contradicts §15.4's `Shutdown` is not held". It does not. The hold begins at the critical section that DEREGISTERS the entry (spec-changes.md:522), so during the hold there is no entry for a `Shutdown` to resolve and `Shutdown` never creates one. §5.2's rule and §15.4's exemption are consistent; the exemption sentence only reads as a carve-out.

FACT: SPEC-3's two "shipped adapter behaviour" claims for the widened §5.2 action list are both true against the tree. `deregisterSlotLocked` cancels every armed direct-mode expiry timer inside the same critical section that deletes the entry (pkg/adapter/slotsession.go:174-183), and `slotlayout.RemoveTree` removes `CredentialsDir` alongside the slot root, `Sessions` and `Artifacts` (pkg/adapter/slotlayout/tree.go:58-69). A reliability round wanting to attack the widened list must attack the ORDER (cancellation at deregistration versus tree removal after the runtime close), which is the recorded Open item on conditioning the timer cancellation, not the membership.

FACT: The "for a hold taken outside any request" arm of the hold's bound is not a contingency — it names a real path. `holdstate.go`'s drain loop deregisters started sessions (`deregisterStartedSessions`, pkg/adapter/slotsession.go:375-395) and then runs `Runtime.Close` plus `removeSlotTree` with no inbound RPC (pkg/adapter/holdstate.go:247-254). The staged bound ("the termination window of the pass that runs the cleanup") covers it.

UNVERIFIED: Whether a `Shutdown` with no client-set deadline leaves the hold unbounded. The staged bound's second arm is "that request's own deadline when it carries none [no graceful window]"; a caller that sets no deadline at all has no bound. I did not trace whether every shipped `Shutdown` caller sets one. Judged below the bar because the remedy is code-lane, but a later reliability or performance round with `Binder.ReleaseSlot` and the §11.4 revoke fan-out open should confirm once.

DIFF NOTE: `diff -rq` between /home/ec2-user/lenny/scratchpad/cp-snap/0081_.../spec-r6 and the proposal directory is EMPTY — round 5 produced no edits at all. A round-7 reviewer told to "read the changed sections hardest" has no changed sections; read the whole staging or nothing.


### [spec.6.review-security.1]

DECISION: returned an empty findings list for the security lens on the staged spec edits — BECAUSE every control the lens owns (zero-RBAC agent pod, §13.2 egress, §13.1 pod security and the credential-file boundary, fail-closed admission, tenant pinning, the acknowledgeProcessLevelIsolation gate, the whole-pod scrub at the recycle boundary, the leaked-slot occupancy hold and the ceil(n/2) replacement trigger) is either untouched by the staged text or moved in the conservative direction — ALTERNATIVES: I built and then killed four candidate findings, each recorded below so nobody rebuilds them.

FACT: the diff against `scratchpad/cp-snap/.../spec-r6` is EMPTY this round (`diff -ru` returns nothing). The snapshot is taken at the round's own start, so "read the changed sections hardest" had no target again. This is now the norm rather than the exception; the standing context already says so at review-log.md's Settled block. EVIDENCE: proposals/0081_.../0081_....review-log.md:132

FACT: the whole-pod scrub survives every new fence. The staged §4.7 row narrows only the two named teardowns ("removes nothing, runs neither teardown"), and the recycle disposition's scrub is a third act that clause three of the shipped handler runs unconditionally, outside `if bound`. So neither the epoch mismatch, nor an `absent` answer, nor the reclaim hold can suppress the tenant-isolation scrub that precedes pod reuse. EVIDENCE: pkg/adapter/session.go:288-291 (`if rc := req.GetRecycle(); rc != nil { s.startPodScrub(rc) }`, outside the `if bound` block); spec-changes.md:242

FACT: the graceful-shutdown gate the staged §4.7 row and §29.4 step 13 state is shipped verbatim, including its rationale. `if !boundRemains { s.drainViaLifecycle(...) }`, with the comment "The signal is pod-global and names no session, so it goes out only when the deregistration left the registry holding no bound entry". Do not re-derive this. EVIDENCE: pkg/adapter/session.go:248-261

FACT: the gateway dials a FRESH adapter connection per call (`DialAdapter: func(addr string) { return adapterclient.Dial(...) }`), so the non-spec lane's per-connection epoch latch is effectively per-attempt rather than per-pod. This is what makes the §4.7.1 caller rule ("A caller names the epoch it holds on a `Shutdown` for that session") safe on a concurrent pod: a latch shared across co-tenants would have made an ordinary end-of-session `Shutdown` carry a co-tenant's epoch and answer `superseded`, i.e. no teardown. EVIDENCE: cmd/lenny-gateway/stores.go:2094-2097; pkg/gateway/podlifecycle/podsession/slotbinder.go:237,:459

FACT: the §11.4 full-revoke fan-out is safe under the epoch. Peer replicas apply a republished `Shutdown` off Redis pub/sub and have received no bind-sequence response, so they hold no epoch and send the unconditional form; the handling replica dials fresh for the same reason. EVIDENCE: spec/11_policy-and-controls.md:263,:270; spec-changes.md:242 ("A request carrying no epoch is the unconditional teardown")

WATCHOUT: four security findings that look real and are not. (1) *The epoch counter restarts at 1 on an adapter restart, so a lagging compensation could collide with a fresh entry's epoch.* Unreachable: the compensation never re-dials, and an adapter restart kills the connection it is pinned to. The no-re-dial rule is stated normatively in the staged §7.1 paragraph, not left to code. EVIDENCE: spec-changes.md:310,:316-322. (2) *The withheld `ReportSessionScrub` on the pre-`running` path stops `sessionsServed` advancing, relaxing the `recycle.maxSessionsPerPod` residual-state bound for binds that already ran `RunSetup` and wrote a credential file.* Not a regression: the shipped handler only calls `reportSessionScrub` inside `if bound`, and today no compensating `Shutdown` is sent on a failed bind at all, so the count never advanced on this path before either. The staged rule declines to add a count rather than removing one. EVIDENCE: pkg/adapter/session.go:243,:278-279. (3) *The bind epoch is an in-pod self-report gating a mandatory teardown, so a compromised adapter can answer `superseded` forever and evade both the reclaim and the leak accounting.* No new trust: `Binder.ReleaseSlot` already sets `leaked = err != nil || !cleanly` from the adapter's own answer, and an adapter that wants to keep state can simply ignore the RPC. The §11.2/§13.2 "no quantity from the pod's self-report widens a bound" principle is about quota-bearing quantities; the epoch widens no bound. EVIDENCE: spec/11_policy-and-controls.md:37; spec/13_security-model.md:196. (4) *A late `StartSession` re-creating the entry after the cleanup leaves an orphan runtime holding live credentials.* The cleanup removed `CredentialsDir` before the re-creation and the §4.9 timers were cancelled at deregistration, so the orphan has no credential file; the residue is a process, and it is an explicitly recorded accepted failure mode. EVIDENCE: pkg/adapter/slotlayout/tree.go:60; spec-changes.md:249-258.

WATCHOUT: §6.2's "Pre-attached failure retry policy" prose literally reads "The pod is marked `failed` and released back to the pool (or terminated if unhealthy)", which on a first read contradicts the staged §7.1 sentence "the pod retires under the §6.2 pre-attached failure disposition" and would be a cross-tenant residue exposure if the pod really re-entered idle inventory. It does not: "marked `failed`" puts the pod on the `failed ──→ draining ──→ terminated` projection, and §5.2's recycle-lifecycle paragraph states "A session that ends in failure or a crash always retires its pod regardless of recycle settings." Standing context already derived the code side (`failPhase` drains the pod on the exclusive path). Do not file this. EVIDENCE: spec/06_warm-pod-model.md:283,:100-104; spec/05_runtime-registry-and-pool-model.md:455

UNVERIFIED: spec/06:160 makes `lenny_adapter_leaked_slots` an ADAPTER health-metadata export ("The adapter exposes a `leaked_slots` count in the pod's health metadata (`/healthz` response and `lenny_adapter_leaked_slots` gauge)"), while the standing context records that in the tree only the gateway's `applySlotRetryPolicy` produces that gauge. The staged §5.2 append asserts a pre-`running` incomplete cleanup "is surfaced on the `lenny_adapter_leaked_slots` gauge" on the strength of the gateway-side path. Whether the adapter's own `/healthz` `leaked_slots` count also increments when the adapter withholds `ReportSessionScrub` is not stated anywhere and nobody has checked it; if it does not, the §6.2 `ceil(maxConcurrentSessions/2)` drain trigger sourced from that count would not see these leaks. I did not file it because the trigger the staged text names is the gateway-side one and the spec/code split on the gauge predates this proposal. A future observability or conformance lens should settle it. EVIDENCE: spec/06_warm-pod-model.md:160; proposals/0081_.../0081_....review-log.md:140


### [spec.7.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens on the spec staging — BECAUSE every mechanical check the lens owns came back clean (anchor uniqueness, replacement absence, markdown-anchor resolution, insertion-point uniqueness, new-term collision, reserved-phrase and line-citation sweeps, tier-11 gate reachability) — ALTERNATIVES: filing the per-entry-epoch inheritance hazard (rejected: it is a mechanism/correctness question, it is on the already-fixed list, and the proposal now records it explicitly as the shared-entry accepted residue at spec-changes.md:231-247), and filing checklist/Depends-on drift (rejected: the brief puts checklist reconciliation outside this loop).

FACT: the r6→r7 delta is exactly five hunks and nothing else. Design line 60-62 (retry-that-meets-the-hold no longer "spends a policy attempt"), Design 147 ("hold is the only bind-path refusal" removed), two edge-case bullets re-pointed off the nonexistent "open decision D1's Option B", the §7.1 paragraph's matching re-word, the §5.2 hold paragraph's bound rewrite (removal of directories now explicitly "under no deadline"), and §4.7.1/§15.4's "what an adapter refuses onto a slot identifier" re-word. Every one of those closed a previously filed finding and introduced no new contradiction I could find. EVIDENCE: diff -u scratchpad/cp-snap/.../spec-r6 vs live.

FACT: the mechanical anchor sweep is now reproducible in ten lines and covers the AMENDED staging too. 31 fenced blocks after `## Staged edits`; blocks 0,2,5,8,10,12,14,16,18,20,22,24 occur exactly once in spec/*.md and every other block occurs zero times. Rerun this rather than eyeballing. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:270 onward.

FACT: every markdown anchor the staged text mints resolves, including the two the amendment added that Settled #73 does not list — `07_session-lifecycle.md#74-upload-safety` and `10_gateway-internals.md#101-horizontal-scaling`. Both exist. EVIDENCE: spec/07_session-lifecycle.md (### 7.4 heading), spec/10_gateway-internals.md:3.

FACT: SPEC-5's two insertion points are unique and unambiguous in the current tree. spec/04_system-components.md:688 is the `*Adapter → Gateway RPCs:*` label, its table closes at :693, and `#### 4.7.2` is at :695; spec/15_external-api-surface.md:1469 is the `**SDK-warm demotion contract:**` paragraph and `#### 15.4.1` is at :1471. Nothing sits between either pair.

FACT: no shipped gate hard-fails on the amended text, re-derived rather than inherited. `TestLeakedSlotCountingLifetimeAgrees_F5231` scans §5.2 with the case-sensitive literal "Whole-pod replacement trigger" while SPEC-3's append writes the lowercase "whole-pod replacement trigger stated below", so the first-match scan still lands on the shipped bullet; `channel_contract_single_owner_test.go` matches `normative prose (description|reference)` and the staged §15.4 wording is "normative for a third-party adapter"; `credential_path_literal_sweep_test.go` sweeps the retired pod-global literal, not the per-slot `/run/lenny/slots/{sessionId}/` SPEC-3 adds. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:88-95; channel_contract_single_owner_test.go:39; credential_path_literal_sweep_test.go:1-19.

WATCHOUT: the snapshot the brief hands you is byte-identical to the live proposal AGAIN (spec-r7 and spec-r7-start both SAME). Run `for d in scratchpad/cp-snap/0081_*/*/; do diff -q $d/*.spec-changes.md proposals/0081_*/*.spec-changes.md; done` and diff against the newest DIFF snapshot (spec-r6 this round). EVIDENCE: standing-context Settled entry on empty snapshot diffs.

WATCHOUT: the bind epoch is minted PER REGISTRY ENTRY in the current text, which reverses the standing-context DECISION recording it as per admitted bind ATTEMPT. This is not stale text — the proposal now owns the consequence in two accepted-failure-mode bullets ("a retry that adopts a surviving entry inherits that entry's epoch", "Such a discriminator would have to be carried on every request that can create or resolve an entry, which this proposal does not stage") and SCHEMA-1 correspondingly adds no request field. A reviewer who trusts the standing context's DECISION will file a per-entry finding that the document already answers. EVIDENCE: spec-changes.md:105-109, :231-247, :678, :680.

CORRECTS [standing-context Settled "DECISION: the bind epoch is minted per admitted bind ATTEMPT, not per registry entry, and is echoed by the caller on the seven bind-sequence REQUESTS"]: the staging no longer does either. §4.7.1 reads "The adapter mints a **bind epoch** when it creates a registry entry for a slot identifier it holds none for" and "none of them carries one on its request"; §15.4 makes refusing a bind-sequence request on an epoch a non-conformance. The per-attempt echo was withdrawn and the residue it would have closed is recorded as accepted. Compaction should replace that entry rather than carry both.


### [spec.7.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in the staged spec edits verified against the tree, including the four explicit file:line citations, all sixteen-plus replacement anchors, and every attributed behaviour I could reach in pkg/ — ALTERNATIVES: I considered filing on §5.2's "The `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` figure ... is not a second bound on the hold" against spec/05:545's "Cleanup timeout is max(...) (minimum 5s enforced at runtime by the adapter)", but that sentence is unchanged from r6, survived seven sweeps, and the closest variant was already refuted this loop; and on §4.7.1's "the generation ... is validated on the RPCs that carry it" against the shipped adapter, which validates it only in CoordinatorFence and CheckpointBarrier — the staged sentence agrees with spec/10:30, so the drift is pre-existing spec-vs-code and outside this proposal.

FACT: the spec-r7 snapshot is byte-identical to the current proposal directory, so `diff -ru .../spec-r7 proposals/0081...` returns nothing. The round-6→7 delta is `diff -u .../spec-r6/...spec-changes.md .../spec-changes.md` and is six hunks: the retry-policy-did-not-place rewording in the Design and in staged §7.1, deletion of "the hold is the only rule that refuses a request on the bind path" from the Design and from staged §5.2, the two D1-Option-B pointers replaced with in-document residue pointers, the hold's bound sentence rewritten so the directory removal runs after the close under no deadline, the §4.7.1 "no refusal onto a slot identifier" reword, the §4.7.1 caller rule rewritten as "carries an epoch only when it compensates", and the §15.4 "refusal an adapter must exhibit" reword. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r7

FACT: the gateway's bind genuinely spans two connections, which is what makes §4.7.1's two-connection caller rule true rather than hypothetical. `Binder.Prepare` calls `cl.Close()` at the end of its success path and `Binder.Launch` re-enters through `b.reconnect`. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:843,955,976,1115

FACT: the already-started refusal the create-time-reserved edge case leans on is `codes.Unavailable` ("session %s has already started on this pod"), i.e. transient, and the SDK-warm different-session refusal beside it is also `Unavailable`. EVIDENCE: pkg/adapter/slotsession.go:70-86

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule that a `DemoteSDK` drops the caller's held epoch is grounded: it calls `noteRuntimeClosed` then `releaseSessionSlot` on `anyRegisteredSession()`. EVIDENCE: pkg/adapter/sdkwarm.go:296-299

FACT: the §4.7 row's new "flushes the session's final usage report and then closes the runtime", with the §15.4.2 signal between them, matches the shipped handler ordering exactly: `emitFinalUsage` → `drainViaLifecycle` (gated on `!boundRemains`) → `Runtime.Close` → `removeSlotTree` → `reportSessionScrub`. EVIDENCE: pkg/adapter/session.go:243-280

FACT: `ShutdownRequest` field numbers in use are 1,2,3,5,6 with 4 reserved (`slot_id`), so the staged `expected_bind_epoch` has free numbers above 6; `deadline_ms` is field 3 and is the "graceful window" the hold paragraph names. EVIDENCE: schemas/lenny-adapter.proto:1609-1635

FACT: all seven named bind-sequence RPCs have unary responses (`PrepareWorkspace` alone is client-streaming with a single response), so §4.7.1's "each of them reports the entry's current epoch on its response" is wire-realisable as written. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151

USEFUL [Standing context / anchor sweep]: the "sixteen sites, every text-to-replace block matches byte for byte" entry saved me re-deriving the whole sweep; I re-checked seven of them by grep (spec/04:157,686; spec/05:453,545,555; spec/06:150,234; spec/07:23,210,213,214,414; spec/29 step 13) and every one still matches, so the entry is still accurate at this commit.

WATCHOUT: the mid-session-upload claim in §4.7.1 is grounded in code rather than in §7.4's prose. spec/07:440-446 never names `PrepareWorkspace`/`FinalizeWorkspace` for a mid-session upload; it says only "the same staging→validation→promotion pattern". The RPC pair is real but the evidence is `pkg/gateway/sessionserver/upload_to_session.go:128,134`, not §7.4. A future reviewer tempted to file "§7.4 does not say that" should read the code first. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:125-134


### [spec.7.review-client-surface.1]

CORRECTS [standing context: "MISTAKE: SCHEMA-1 asserts `AssignCredentialsResponse` is an empty
message today, so its first field is 1"]: that entry is WRONG and dangerous. It confused
`AssignCredentialsResponse` with `RotateCredentialsRequest`. `AssignCredentialsResponse` really is
empty (`message AssignCredentialsResponse {}` at schemas/lenny-adapter.proto:1033); the
`session_id = 1 / leases = 2 / rotation_trigger = 3 / reserved 4 / coordination_generation = 5`
field set the entry quotes is `RotateCredentialsRequest`, schemas/lenny-adapter.proto:1035-1053.
SCHEMA-1's `bind_epoch = 1` on `AssignCredentialsResponse` is correct and free; "correcting" it to
6 would be the defect. EVIDENCE: schemas/lenny-adapter.proto:1033,1035-1053.

FACT: all nine SCHEMA-1 field numbers were re-checked against the shipped proto and every one is
free, with no reserved-number reuse and no rename. Used numbers today: `ShutdownRequest` 1,2,3,
reserved 4, 5,6 (staged 7); `ShutdownResponse` 1,2 (staged 3); `PrepareWorkspaceResponse` 1,2
(staged 3); `FinalizeWorkspaceResponse` 1 (staged 2); `ResumeResponse` 1,2,3 (staged 4);
`ConfigureWorkspaceResponse` 1 (staged 2); `RunSetupResponse` 1 (staged 2); `StartSessionResponse`
1 (staged 2); `AssignCredentialsResponse` empty (staged 1). EVIDENCE:
schemas/lenny-adapter.proto:1033 and the `message …Response`/`message ShutdownRequest` blocks.

FACT: the gateway↔adapter proto has NO parallel client representation to keep in step. There is no
copy of `lenny-adapter.proto` outside `schemas/` and the `scripts/specshift/testdata/` fixtures, no
SDK or docs file mentions `ShutdownRequest` or `AssignCredentialsResponse`, and no `spec/` or
`docs/` text enumerates the wire field names of the seven bind-sequence responses
(`staged_bytes`, `refusal_reason`, `restored_bytes`, `workspace_plan_warnings`, `exited_cleanly`
all return nothing in spec/ or docs/). So SCHEMA-1's nine fields and one enum mirror nowhere but
`pkg/gen/adapter/v1`, which is generated. EVIDENCE: `find . -name "*.proto"`;
`grep -rln "ShutdownRequest" sdks/ docs/` returns nothing.

FACT: the intra-pod graceful-shutdown JSONL frame genuinely names no session, so SPEC-1's §29.4
step-13 sentence and the §4.7 row's pod-global clause are true against the published schema:
`$defs.shutdown` requires only `type`, `reason`, `deadline_ms`. EVIDENCE:
schemas/lenny-adapter-jsonl.schema.json `$defs.shutdown`.

FACT: §15.4.6's conformance battery really is runtime-facing (it starts the runtime binary against
a fake adapter and probes integration level), so the proposal's reason for leaving it untouched and
putting CONF-1 in tier 10 holds. EVIDENCE: spec/15_external-api-surface.md:2038-2048.

FACT: `examples/runtimes/echo/`, which spec/15_external-api-surface.md:1466 calls "A Go reference
implementation of the adapter … built from the same `.proto` file", DOES NOT EXIST in the tree
(`examples/` is absent). Pre-existing stale spec claim, not this proposal's; do not chase it as a
missing adapter-side edit site for the bind epoch.

WATCHOUT: §5.2's staged reclaim-hold paragraph and §15.4's published hold contract now BOTH use the
wide predicate ("no request that would create or resolve a registry entry"), not "no bind". A
refuted finding in the pool is written against the older "no bind" wording of §5.2 and its
refutation argues the phrase is "the change's own vocabulary for the bind-sequence RPCs"; that
argument is weaker against the current text, because `Attach`, `Interrupt`, `SendMessage`,
`Checkpoint`, `ExportPaths`, `ReportUsage` and the three credential RPCs all resolve the entry too.
I did not file it: during the hold the entry is deregistered, so those RPCs fail anyway and the only
delta is the status code they fail with, which no spec text fixes today. EVIDENCE:
proposals/0081_…spec-changes.md:601 ("admits no request that would create or resolve a registry
entry under it"), :711 (§15.4, same predicate plus `ABORTED`).

FACT (checked, clean): the three statements of the epoch contract agree. §4.7.1 lists exactly the
seven bind-sequence RPCs and all seven are rows in the §4.7 Gateway→Adapter table
(spec/04_system-components.md:668-686); `PrepareWorkspace` is the only client-streaming one
(schemas/lenny-adapter.proto:41), which is what §4.7.1's single-resolution rule needs; `DemoteSDK`
really does remove the entry (pkg/adapter/sdkwarm.go:298 `s.releaseSessionSlot(sessionID)`), which
is what the caller rule's "holds none" clause rests on; and `Shutdown` appears in no §28 register
row (`grep -n "Shutdown" spec/28_communication-channels.md` is empty), so the "§28 untouched" claim
holds. All four anchors named in SPEC-5's insertion instructions resolve
(spec/04:659,:688,:695; spec/15:1469,:1471).

DECISION: returned an empty findings list. BECAUSE every client-facing surface this proposal
touches is either the adapter proto (verified complete and internally consistent with the staged
§4.7/§4.7.1/§15.4 text), or has no parallel representation to drift from. ALTERNATIVES rejected:
the wide-predicate hold finding (close variant of a refuted one, see WATCHOUT); an
`AssignCredentialsResponse` field-number finding (the standing-context entry that suggests it is
itself wrong); a §15.4 "reports zero on a bind-sequence response" carve-out for an empty
`PrepareWorkspace` stream (too thin, and the gateway sends that RPC only when the plan carries
uploads).


### [spec.7.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE this loop's scope admits only findings whose fix lands in `spec-changes.md`, and the docs-alignment lens's two in-scope classes are both exhausted here: (a) every "Edge cases and accepted failure modes" row resolves to a staged spec sentence or to a deliberate, recorded scope call, and (b) the "accepted outcome lands in no staged spec text" shape has already been filed twice and refuted twice by the material skeptic. ALTERNATIVES: filing the `lenny_adapter_leaked_slots` gauge as an unmirrored metric (see FACT below) and filing the `docs/reference/adapter-contract.md:75` drift (fix lands in the docs lane, out of scope this loop).

FACT: `lenny_adapter_leaked_slots` is ALREADY named in the shipped spec twice, so SPEC-3's `**Scrub model.**` clause naming it mints no new metric mention and owes spec/16 and docs/reference/metrics.md nothing new. — EVIDENCE: spec/06_warm-pod-model.md:160 ("the persistent count is the `lenny_adapter_leaked_slots` gauge"); spec/05_runtime-registry-and-pool-model.md:545 ("surfaced via the `lenny_adapter_leaked_slots` gauge"). This corrects the natural reading of Settled entry 229 ("The gauge is absent from spec/16 and docs/reference/metrics.md, which is pre-existing"), which is true of spec/16 but is NOT a statement that the gauge is unmentioned in `spec/`. A lens reaching for a metric-mirror finding stops here.

FACT: SPEC-5's two insertion anchors both resolve and both land inside the section their headings name. §4.7.1 spans spec/04_system-components.md:659-694 (`#### 4.7.1` at :659, `#### 4.7.2` at :695), so "after the `*Adapter → Gateway RPCs:*` table closes" (table ends :693) is inside §4.7.1. §15.4's anchor paragraph is spec/15_external-api-surface.md:1469 and `#### 15.4.1` is at :1471. — EVIDENCE: spec/04_system-components.md:659,688-695; spec/15_external-api-surface.md:1469,1471.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller-drop rule cites a true behaviour. — EVIDENCE: pkg/adapter/sdkwarm.go:287,:297-298 (`s.sdkConnected = false`, `noteRuntimeClosed`, `releaseSessionSlot`); spec/04_system-components.md:674 (the `DemoteSDK` row's "return the pod to pod-warm state").

FACT: the tier-11 single-owner gate cannot fire on SPEC-5's §15.4 block. Its matcher is the regexp `normative\s+prose\s+(?:description|reference)`; the staged text says "That statement is normative for a third-party adapter and is not restated here", which does not match. — EVIDENCE: tests/tier11_docs/channel_contract_single_owner_test.go:38; spec-changes.md:700.

WATCHOUT: the round-7 delta is small and entirely repair of earlier fixer text — five hunks, all in prose that already survived review. `diff -u scratchpad/cp-snap/.../spec-r6/*.spec-changes.md proposals/.../*.spec-changes.md` is 86 lines. The `spec-r7` snapshot the brief names is byte-identical to the live proposal, as Settled entry 134 warns; diff against `spec-r6`. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r6.

DEFERRED [non-spec / docs lane]: `docs/reference/adapter-contract.md:75` still describes the `Shutdown` teardown as one act gated on the binding, which SPEC-1's two-teardown split falsifies, and Settled entry 165 records that no tier-11 gate catches it. Nothing in the spec staging can fix that; the docs lane owns the row. Recorded here so the between-loops pass can confirm the docs staging carries it rather than assuming a lens filed it.


### [spec.7.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site-completeness lens — BECAUSE every identifier and concept the staged spec edits add or change was grepped across spec/, schemas/, docs/ and charts/, and every spec surface that mentions one is either already in an edit list or is unaffected — ALTERNATIVES: filed nothing on three candidates I judged below the materiality bar (see the three WATCHOUT/OPEN entries below), each of which a material skeptic would refute as documentation completeness on pre-existing text.

FACT: `diff -ru scratchpad/cp-snap/.../spec-r7 proposals/0081_.../` returns EMPTY at round 7, so the round-6 fixer landed no edits. Nothing in the document is "fix-stage text" this round; the reading-order advice about newest text does not apply. EVIDENCE: proposals/0081_.../*.spec-changes.md mtime 2026-09-10 11:55, snapshot identical.

FACT: every verbatim anchor SPEC-1..SPEC-5 quotes is exact in the tree at round 7. Verified: spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown` row opening), spec/04 §4.7.1 insertion point is between the `*Adapter → Gateway RPCs:*` table (ends :693) and `#### 4.7.2` (:695), spec/04:848 §4.7.9 step 5, spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**` pod-selection sentence), spec/06:152-153 (`receiving_uploads ──→ running`), spec/06:156 fence close / :158 `**`reserved` hold semantics.**`, spec/06:234 (mid-resume cancel bullet clause), spec/07:23 (atomicity parenthetical), spec/07:210/:213/:214 (§7.2 preamble, step 2, step 3), spec/07:414 (§7.3 list tail), spec/15:1469 (SDK-warm demotion contract) / :1471 (`#### 15.4.1`), spec/29:704-711 (§29.4 step 13). Every markdown anchor the staged text links resolves to a real heading.

FACT: the two "deliberately untouched" claims a lens would most want to break both hold. §28 carries no `Shutdown` row and no bind-sequence RPC row — §28.5.1's gateway-to-pod card set is exactly CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER, CH-PODHEALTH, and the whole file's channel set has no bind-sequence channel. EVIDENCE: spec/28_communication-channels.md:211-222; `grep -o "CH-[A-Z-]*" spec/28 | sort -u`. §15.4.6's conformance battery runs the runtime binary against a fake adapter, so it has no adapter under test. EVIDENCE: spec/15_external-api-surface.md:2046 "Start the runtime against the fake adapter".

FACT: all seven "bind-sequence" RPCs really do create-or-resolve the adapter's registry entry, so §4.7.1's "each reports the entry's current epoch" has a referent on every one. `RunSetup`, `FinalizeWorkspace` and `PrepareWorkspace` reach the registry through `ensureSlotPaths` (which calls `ensureSlotStateLocked`), not only the three claim-path RPCs. EVIDENCE: pkg/adapter/slot.go:134-147; pkg/adapter/staging.go:134,:181,:337. `PrepareWorkspace` resolves the slot identifier exactly once, from the first frame, so the §15.4 non-conformance clause "resolves the slot identifier more than once within one `PrepareWorkspace` call" does not fire on the first-party adapter. EVIDENCE: pkg/adapter/staging.go:75-83 (`if stagingDir == ""` guard).

FACT: SPEC-3's two added slot-cleanup actions are both shipped behaviour, as the proposal claims. `slotlayout.RemoveTree` removes `CredentialsDir` alongside the slot root, sessions and artifacts dirs; `deregisterSlotLocked` cancels every armed expiry timer before deleting the entry. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69; pkg/adapter/slotsession.go:174-189. The credential path `/run/lenny/slots/{sessionId}/credentials.json` the staged text writes matches spec/04_system-components.md:1169.

WATCHOUT: SPEC-3's replacement action list names four acts plus `slotId`, but `slotlayout.RemoveTree` also removes `/sessions/{sessionId}` and `/artifacts/{sessionId}`, and the staged §4.7 row elevates that list ("The row does not enumerate the slot release's actions; §5.2 owns that list"). I did NOT file this: the omission predates the proposal, spec/06:363's tmpfs guarantee and the §5.2 whole-pod scrub cover the residue, and no citation or mechanism breaks. A future round that wants to widen the list should widen it as a deliberate improvement rather than as a defect. EVIDENCE: pkg/adapter/slotlayout/tree.go:60; proposals/0081_....spec-changes.md:578.

WATCHOUT: the staged edits use "clean exit" / "clean-exit response" as a load-bearing predicate in three places (§4.7 row, §7.1 "answered `reclaimed` without reporting a clean exit", §5.2 "does not report a clean exit") and the spec defines the term nowhere — `grep -rn "clean exit\|exited_cleanly" spec/` returns nothing. It maps to `ShutdownResponse.exited_cleanly` (schemas/lenny-adapter.proto:1665-1668). I did NOT file it: the term predates the bind-epoch amendment, survived seven sweeps and eleven lenses, and §15.4's third-party contract states the three outcomes without needing it. Anyone who files it should expect the materiality default.

WATCHOUT: spec/04_system-components.md:1538 asserts "The `leaseToken` field in `AssignCredentialsResponse` is unchanged", but `AssignCredentialsResponse` is an empty message today (schemas/lenny-adapter.proto:1033) and SCHEMA-1 gives it `bind_epoch = 1`. The sentence is already false before this proposal and is about SPIFFE-binding rather than about the message's field set, so it is not a site this proposal makes wrong. Do not file it as an unstaged edit site.

FACT: `lenny_adapter_leaked_slots` lives only in spec/05:545 and spec/06:160 — it is in neither spec/16's metric inventory nor docs/reference/metrics.md. That gap is pre-existing and the proposal adds no metric, so the metric/inventory companion pair has nothing to reconcile. EVIDENCE: `grep -rn lenny_adapter_leaked_slots spec/ docs/ pkg/alerting/`.

FACT: the §29.4 step-13 append's premise checks out on the wire. The `terminate` CH-RUNTIMEOPS frame carries only `type`, `deadlineMs` and `reason` and names no session, so "pod-global and names no session" is accurate. EVIDENCE: schemas/runtime-ops-events.schema.json:174-185.

OPEN: a `PrepareWorkspace` call with zero frames resolves no entry, so its response would report epoch zero, which the staged §15.4 clause "reports zero on a bind-sequence response" calls non-conformant. The gateway never sends an empty stream (`stageWorkspace` sends `PrepareWorkspace` only when the plan carries uploads), so the case is unreachable today; a later round that wants to close it should scope the §15.4 clause to a call that resolved an entry rather than restate it. EVIDENCE: pkg/adapter/staging.go:56-83; proposals/0081_....spec-changes.md:154.


### [spec.7.review-feasibility.1]

DECISION: returned an EMPTY findings list for the actor-action-feasibility lens on the staged spec edits — BECAUSE every actor the staged text names can perform the action assigned to it, verified against the tree, and no §4.6.3 ownership, §10.3 zero-RBAC, §13.2 egress, webhook-purity, deployment-model or spec/18 phase-ordering constraint is engaged by any staged sentence. ALTERNATIVES: I built and then killed three candidate findings (below); each failed on evidence.

FACT: the r6→r7 spec-changes delta is five hunks and all five are prose tightenings, not new mechanism: the §7.1/Design "spends one of the attempts" → "one that policy did not place" rewrite (:61-62, :390), the deletion of "the hold is the only rule that refuses a request on the bind path" (:148) and of its §5.2 twin (:602), the two D1-Option-B pointer rewrites in the edge cases (:188, :244), the §5.2 hold's bound restated as close-bounded + unbounded directory removal (:602), and the §4.7.1 caller rule restated as "A `Shutdown` carries an epoch only when it compensates a bind attempt the caller abandoned" (:682). EVIDENCE: diff scratchpad/cp-snap/0081.../spec-r6 vs proposals/0081... (spec-r7 and spec-r7-start are byte-identical to live).

FACT (killed candidate 1). §4.7.1's "the generation ... is validated on the RPCs that carry it ... Where both appear on one message, as on `Shutdown`, each is checked on its own terms" (spec-changes.md:678) looks false against the shipped adapter, which reads `coordination_generation` only in `CoordinatorFence` and `CheckpointBarrier` and never in the `Shutdown` handler. It is NOT a spec defect: spec/10_gateway-internals.md:30 already states "Pods validate the generation on every gateway→pod RPC — if the generation is stale, the pod rejects the request". The staged sentence restates shipped SPEC, and the code gap is pre-existing. EVIDENCE: spec/10_gateway-internals.md:30; pkg/adapter/coordination.go:120,:262; pkg/adapter/session.go:227-278 (no generation read).

FACT (killed candidate 2). The §4.7.1 caller rule's "`DemoteSDK` ... and removes the entry" cites `[Section 4.7](#47-runtime-adapter)`, and spec/04:674's `DemoteSDK` row says only "Tear down the pre-connected SDK process and return the pod to pod-warm state" — it does not mention the registry entry. The citation is still sound, because it is attached to the pod-warm clause, and the entry removal is true in the tree: `DemoteSDK` calls `s.releaseSessionSlot(sessionID)`, which deregisters and calls `removeSlotTree` synchronously inside the RPC. That also makes §5.2's "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it" (spec-changes.md:602) mechanically true. EVIDENCE: pkg/adapter/sdkwarm.go:274-302; pkg/adapter/slotsession.go:214-220; spec/04_system-components.md:674.

FACT (killed candidate 3). §4.7.1's two-connection clause ("as the §4.7.9 step-5 bind sequence does when the gateway splits it across a prepare and a launch", spec-changes.md:682) is accurate for the EXCLUSIVE pool only, and that is the only pool it can be about: `prepareAtFinalize` returns `(nil, nil)` for `match.MaxConcurrentSessions > 1` with the comment "a concurrent-workspace pool ... is materialized and launched together at /start via BindReservedSlot rather than decomposed across finalize and start", so the split is exactly the exclusive finalize/launch pair. EVIDENCE: pkg/gateway/sessionserver/finalize.go:196-199,:236-238.

FACT: all seven RPCs §4.7.1 names as the bind sequence are unary except `PrepareWorkspace` (client-streaming), so "the call's single response reports that entry's epoch" is well formed for every one of them and no server-streaming case is left undefined. All seven are rows in the §4.7 Gateway→Adapter table. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151; spec/04_system-components.md:669-685.

FACT: every anchor the staged text mints or reuses resolves, including the two that post-date the standing-context sweep — `#74-upload-safety` (spec/07_session-lifecycle.md:438) and `#101-horizontal-scaling` (spec/10_gateway-internals.md:3). SPEC-5's two insertion points are exact: the `*Adapter → Gateway RPCs:*` table closes at spec/04:695 immediately before `#### 4.7.2` at :697, and the `**SDK-warm demotion contract:**` paragraph is spec/15:1469 immediately before `#### 15.4.1` at :1471. SPEC-4's fence anchor is exact: the either-concurrency header is spec/06:150 and `receiving_uploads ──→ running` is :152-153.

FACT: the §7.4 mid-session upload really does reuse `PrepareWorkspace` and `FinalizeWorkspace`, so §4.7.1's mid-session clause attributes the right RPCs to the right path. §7.4's own prose never names them; the tree does. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:64-66 ("the bytes into the session's staging tree and FinalizeWorkspace(midSession)").

WATCHOUT: the staged proto fields are bare `int64` (`ShutdownRequest.expected_bind_epoch` = 7, seven response `bind_epoch` fields), so SPEC-1's §4.1 rationale — "a field that has no wire presence cannot engage" the presence-standing-in-for-a-scope rule — is true only while nobody makes them `optional`. If a later fixer adds `optional` for tri-state clarity, SPEC-1's whole §4.1 argument collapses. EVIDENCE: non-spec-changes.md:1091-1099; spec-changes.md:288-294.

WATCHOUT: §5.2's hold sentence "the adapter admits no request that would create or resolve a registry entry under it" reads, on its face, as reaching `Attach`, `Interrupt`, `ReportUsage` and every other entry-resolving RPC, while the code lane implements the refusal at five bind-path resolve sites only. Do NOT file this: an evidence skeptic has already adjudicated "create or resolve" as this change's own vocabulary for the bind-sequence RPCs, citing spec-changes.md:680's "A bind-sequence RPC that creates the entry ... one that resolves an entry the adapter already holds", and a re-file is a close variant of a refuted finding.

WATCHOUT: §6.2's `slot_cleanup ──→ released` fence annotation still glosses the cleanup as "(slot workspace removed, processes killed, slot released)" while SPEC-3 widens §5.2's action list to five acts (adds the credential directory and the §4.9 timers). It is a gloss rather than an enumeration and §5.2 owns the list, so I judged it below the bar; a later edit-sites lens that wants to file it should say why the gloss is an enumeration. EVIDENCE: spec/06_warm-pod-model.md:156; spec-changes.md:579.

USEFUL [Settled #64]: the "`Shutdown` is unfenced ... the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`" entry is what made candidate 1 look like a finding; the entry is right about the code and silent about spec/10:30, which is what refutes the finding. Extend that entry with the spec/10:30 half so the next lens does not re-derive it.

USEFUL [Settled #75, #232]: the anchor sweep entries saved re-deriving twelve of the fourteen anchors; only the two new ones needed a read.


### [spec.7.review-fresh.1]

DECISION: returned an EMPTY findings list for the fresh-holistic lens on the staged spec edits — BECAUSE the two candidates I could substantiate are both wording-grade against the standing materiality bar and both are close relatives of findings the material skeptic already refuted in this window — ALTERNATIVES: filing either; rejected because each costs two verifiers and neither makes the applied spec unimplementable.

FACT: the mechanical anchor sweep is CLEAN on the current text and I re-ran it rather than trusting the log. A ten-line python pass over the fenced blocks of the `## Staged edits` section against `spec/*.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24 and count 0 for every replacement/insertion block (1,3,4,6,7,9,11,13,15,17,19,21,23,25,26,27,28,29,30). Every anchor is unique repo-wide. The two SPEC-5 insertion points are real and unambiguous: spec/04_system-components.md:688 (`*Adapter → Gateway RPCs:*` table) closing before :695 `#### 4.7.2`, and spec/15_external-api-surface.md:1469 (`**SDK-warm demotion contract:**`) before :1471 `#### 15.4.1`. EVIDENCE: spec/04_system-components.md:688,695; spec/15_external-api-surface.md:1469,1471.

FACT: every file:line citation IN `.spec-changes.md` resolves. There are exactly four: `schemas/lenny-adapter.proto:1630-1635` (the `coordination_generation` comment plus `int64 coordination_generation = 6;` — exact), `spec/04_system-components.md:151` (the field-set derivation paragraph), `:157` (the `ShutdownRequest` paragraph, the SPEC-1 anchor), and `spec/29_communication-scenarios.md:586-588` (the §29.4 `**Preconditions.**` sentence). Do not re-verify these. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:290,301,306,352.

FACT: the seven bind-sequence RPCs are exactly the create-or-resolve set and I re-derived it from the tree, not from the log. `grep -n "ensureSlotStateLocked\|ensureSlotPaths" pkg/adapter/*.go` (non-test) returns staging.go:134,:181,:337, slotcreds.go:26, slotsession.go:75 and the two definitions in slot.go; slotsession.go:75 is reached from session.go (StartSession), resume.go (Resume) and sdkwarm.go (ConfigureWorkspace). There is no eighth. The proto confirms `PrepareWorkspace` is the only client-streaming member (`rpc PrepareWorkspace(stream PrepareWorkspaceRequest) returns (PrepareWorkspaceResponse)`), so §4.7.1's single-response clause is accurate. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151,206.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule ("`DemoteSDK` ... removes the entry") is grounded rather than assumed. The handler calls `s.noteRuntimeClosed(sessionID)` then `s.releaseSessionSlot(sessionID)` for the registry's single entry, inside the RPC, which is also what makes §5.2's "a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" true on the demotion path. EVIDENCE: pkg/adapter/sdkwarm.go:296-301.

FACT: the §7.4 mid-session upload genuinely reuses two bind-sequence RPCs on a live binding, so §4.7.1's mid-session clause is not a guess. The handler calls `bind.Adapter.PrepareWorkspace(ctx, row.ID, uploads)` then `bind.Adapter.FinalizeWorkspace(ctx, row.ID, plan, nil, true)` over the coordinating replica's live `podsession.BindResult`. Note the connection is the LIVE binding rather than a bind attempt's own dial, which matters for any future per-connection epoch-latch argument. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:110-137.

WATCHOUT: the round-7 fix to the reclaim hold's bound flipped the paragraph from "bounded, and the bound also covers the directory removal" to "the removal ... runs after that close under no deadline, so the hold outlasts the window", while the untouched `**Slot cleanup:**` bullet in the SAME section still reads "Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter) ... If cleanup fails, the slot is leaked". The staged paragraph also says "The hold lasts as long as the cleanup it covers" and then that the figure "is not a second bound on the hold", which are hard to hold together. I did NOT file it: the harm is nil because the held identifier is the session identifier, so no other session can collide with it (see the standing MISTAKE-nearly-filed on the hold's invisibility), and the material skeptic refuted the sibling finding on the same ground. A future round that wants it must argue from a harm the identifier-scoping does not kill. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:601; spec/05_runtime-registry-and-pool-model.md:545.

WATCHOUT: "a precondition on the whole request" is used for the epoch in two staged blocks while the same proposal's §4.1 replacement says the whole-pod scrub runs "when the recycle disposition is set" with no epoch gate, so the applied spec does not say what a mismatched-epoch `Shutdown` carrying a recycle disposition does to the scrub. Unreachable today (the compensation is the only epoch-carrying `Shutdown` and it never carries the recycle disposition; the occupancy-zero recycle is a separate RPC), and the §4.7 row immediately defines the phrase as gating the slot release and the runtime teardown only. Declined as wording. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:286 (§4.1 replacement), :321 (§4.7 row), :679 (§4.7.1).

FACT: the epoch mechanism, on the per-ENTRY minting the current text restores, still fences the ordering the amendment exists for, and the reachability argument is worth recording so nobody re-opens it. The predecessor's entry must be gone for a successor to mint a new epoch, and the shipped adapter removes it itself on every pre-`Runtime.Start` failure branch (`releaseSessionSlot` at session.go:133,:147,:157), so "entry released, successor creates a fresh entry at a higher epoch, lagging compensation compares unequal and is answered `superseded`" is a reachable ordering rather than a theoretical one. The unfenced ordering is the one where the entry SURVIVES and the retry adopts it, which the proposal records as the shared-entry accepted residue. EVIDENCE: pkg/adapter/session.go:133,147,157; proposals/0081_.../0081_....spec-changes.md:232-244.

USEFUL [Standing context, Settled #316 and #230]: the "create or resolve is much wider than the bind sequence" entry and the mechanical anchor-sweep recipe each saved a full pass. The first is what let me drop the §15.4 hold-scope candidate without rebuilding the refuted argument; the second turned the anchor check into one command.

MISTAKE (mine, nearly): I started to file the §15.4 reclaim-hold block's ABORTED refusal as over-reaching every entry-resolving RPC (`Attach`, `Interrupt`, `SendMessage`, `Checkpoint`, `ReportUsage`, `RotateCredentials`, ...) rather than the seven bind-sequence RPCs the design means. It dies on the refuter's ground already in the log: "create or resolve" is this change's own vocabulary for the bind-sequence RPCs, fixed by §4.7.1's own sentence pair. Anyone re-deriving it should stop here.


### [spec.7.review-kubernetes.1]

DECISION: Returned an empty findings list for the Kubernetes-idiom lens on the spec staging — BECAUSE the staged spec edits touch no Kubernetes surface at all: `grep -ni "controller|finalizer|webhook|reconcil|\.status|CRD|etcd|apiserver|RBAC|informer|watch"` over the whole spec-changes file returns exactly one hit, and it is the untouched-text disclaimer at spec-changes.md:620 ("CRD validation rule ... stand as written"). Every mechanism the amendment adds (bind epoch, reclaim hold, `Shutdown` outcomes, the §6.2 sub-state edge) lives on the gateway↔adapter gRPC surface or in an in-process registry. ALTERNATIVES: I considered filing (i) the gateway-replica-crash hole in the §7.1 reclaim obligation as a missing level-triggered backstop, and (ii) the widened `leaked` class as a stuck-object footgun; both are closed by existing spec text (see FACTs below), so neither meets the bar.

FACT: The `leaked`-slot residue this proposal widens leaves the per-pod `SandboxClaim` in `bound`, but that is NOT an undeletable object: WarmPoolController `GarbageCollect` orphan predicate 1 drains any `bound`/`recycling` claim whose last binding-state transition is older than `claimOrphanTimeout` (default 5m, `--claim-orphan-timeout`) and whose pod no active session references, and it runs on the elected leader only. That is the level-triggered backstop for every edge-triggered reclaim this proposal stages, including the case where the gateway replica holding the connection dies before it can send the compensation. EVIDENCE: spec/04_system-components.md:515-517.

FACT: `SandboxClaim` deliberately omits an `ownerReference` (claim deletion is an explicit lifecycle step), which is why predicate 1 exists at all. EVIDENCE: spec/04_system-components.md:395,515.

FACT: The one-session-pod disposition the proposal leans on resolves to a real pod retirement: §6.2's "Pre-attached failure retry policy" marks the pod `failed` and releases it, and the pod-level fence carries `failed ──→ draining ──→ terminated`. EVIDENCE: spec/06_warm-pod-model.md:282 (policy), :101-102 (edges).

FACT: The per-slot sub-state fence is genuinely two blocks with different concurrency scoping, and the staged new edge lands in the "a pod of either concurrency" block while `slot_cleanup ──→ leaked` stays in the concurrent-occupancy-only block. Both heading strings are unique. EVIDENCE: spec/06_warm-pod-model.md:146 and :150.

FACT: No per-slot sub-state is projected onto a CRD. §6.2 states explicitly that `Sandbox.status.phase` carries only the coarse pod-occupancy phase the WarmPoolController writes and that "the fine session-lifecycle states are not projected onto the CRD", so SPEC-4's new edge creates no second writer of any status subresource and no CRD-as-message-bus surface. EVIDENCE: spec/06_warm-pod-model.md:294.

WATCHOUT: `diff -ru scratchpad/cp-snap/.../spec-r7 proposals/0081_...` returned NOTHING this round — the proposal text is byte-identical to the round-6 snapshot. Do not spend a budget looking for "what changed since last round" text; there is none, and the orchestrator's "read the changed sections first" instruction has no target in round 7.

USEFUL [Standing context, "Ownership is clean."]: That entry ("The gateway owns `SandboxClaim.spec` and `.status` and routes the unhealthy-threshold drain through the `lenny.dev/drain-request` annotation rather than writing `Sandbox.status`. No component writes another's status subresource and no finalizer or SSA force-ownership is involved.") is correct and I re-derived it against spec/04:619 and :630. It saved a full pass over the §4.6.3 ownership table.


### [spec.7.review-mechanism.8]

FACT: The exclusive create path really does split the §4.7.9 step-5 bind sequence across TWO adapter connections, and `Binder.Prepare` CLOSES its connection on SUCCESS before returning. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:841 ("The adapter connection is closed before Prepare returns; Launch reconnects"), :956 `cl.Close()` immediately before the `return &PrepareResult{...}` at :957. This confirms §4.7.1's "when the gateway splits it across a prepare and a launch" clause is NOT a hypothetical; it is the shipped exclusive path.

FACT (the finding): the gateway's `/finalize` handler has two "Gap 2" branches that fail AFTER `Binder.Prepare` succeeded and closed its connection — the `finalizing → ready` store Update at pkg/gateway/sessionserver/sessionserver.go:3167-3181 and the upload-token `ConsumeDigest` at :3188-3197. Both call `reclaimFinalizedPod`, which is `ReclaimClaimed`: it deletes the per-pod claim and revokes the §4.9 lease and sends the adapter NO RPC (finalize.go:379-388; binder.go:1097-1106). Staged §7.1 (spec-changes.md:390) names "the creation finalize block" as a path that owes the pod-side reclaim, mandates it "on the connection the failed stage still holds", and separately forbids re-dialling. On this path no connection is held, so the obligation cannot be discharged as written. Filed.

WATCHOUT: do NOT extend that finding into "and the pod does not retire either". `ReclaimClaimed` returns the pod to the pool rather than draining it (Settled #87), which makes §7.1's exclusive-pod clause look false here too, but the exclusive-pod-retirement family has been refuted eight times (standing Trap at review-log.md:370) and a verifier will merge the two. Keep the finding on the connection, not on the pod disposition.

WATCHOUT: the summary's out-of-scope row "The exclusive path leaves the same residue by a different trigger" (summary.md:472-485) reads as though it already covers the Prepare/Launch gap. It does not: its trigger is explicitly "a bind that succeeded" (prepared and never launched), and it says CODE-4 "has no failure site to hang off". The Gap-2 branches ARE a failure site inside the finalize block §7.1 names, with a compensation the gateway already runs there. Do not let that row refute the finding.

FACT: `Binder.Prepare` is reached from exactly one production site outside the combined `Bind` helper — pkg/gateway/sessionserver/finalize.go:280 (`prepareAtFinalize`, exclusive path) — plus start.go:2634 where `Prepare` and `Launch` are called back to back with nothing between them. So the reachable window is the decomposed create/finalize/start lifecycle on an exclusive pool.

FACT: the concurrent path does NOT have this window. `materializeSlot` keeps one `cl` open from `PrepareWorkspace` through `StartSession` and hands it back to the caller (slotbinder.go:230-254, :450-478), so every concurrent bind stage that can fail holds a connection.

FACT: `DemoteSDK` really does deregister the entry and remove the slot tree synchronously inside the RPC (pkg/adapter/sdkwarm.go:294-301 → `releaseSessionSlot` at slotsession.go:214-220, which calls `deregisterSlot` then `removeSlotTree`), so §5.2's "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" is true of the one path it names.

FACT: all seven bind-sequence RPCs really do create-or-resolve a registry entry, and `PrepareWorkspace` is the only client-streaming one among them, so §4.7.1's per-RPC statements and its `PrepareWorkspace` carve-out are complete against the tree. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151; pkg/adapter/staging.go:31,159,317 (all reach `ensureSlotPaths`), slotcreds.go:26, slotsession.go:75.

FACT: each bind attempt dials its OWN adapter connection (`DialAdapter` at slotbinder.go:237 and :459, binder.go:1152 and :1776), so the per-connection epoch latch cannot pick up a different session's epoch on a concurrent pod. The "connection-scoped versus session-scoped caller rule" angle is closed; do not re-derive it.

MISTAKE (nearly filed, three dresses, all dropped): (1) "§6.2's `slot_cleanup ──→ leaked` annotation says 'cleanup timeout exceeded' while staged §5.2/§7.1 add two more triggers" — barred by the standing Trap at review-log.md:384. (2) "a `Shutdown` carrying BOTH an epoch and the recycle disposition leaves the whole-pod scrub's disposition ambiguous between §4.7's 'precondition on the whole request' and §4.1's 'runs the whole-pod scrub when the recycle disposition is set'" — unreachable, because §4.7.1 restricts the epoch to a compensation and the recycle disposition is only ever sent on the occupancy-zero `ShutdownRecycle`. (3) "§15.4 calls `ABORTED` 'the transient classification a caller retries on' while `isTransientPodClaimError` treats no gRPC code as transient on the resume path" — true of the resume path but a close variant of the already-refuted status-code finding, and the remedy is gateway code rather than spec text.

UNVERIFIED: whether the §7.1 fix for the Gap-2 window should relax the no-re-dial rule for the epochless form (a re-dialled compensation holds no epoch and is therefore already the unconditional teardown, which is exactly what the §4.7 escape hatch defines), or instead record the window as an accepted residue. Both are legal; the fixer must pick one and make §7.1, §4.7.1's caller rule, and the accepted-failure-mode list say the same thing.


### [spec.7.review-operational.1]

DECISION: returned an EMPTY findings list for the operational-consistency lens on the spec staging — BECAUSE every observability, condition-ownership and operator-facing surface the staged edits touch is either already correct, already adjudicated in the log, or a pre-existing hole this proposal neither creates nor widens — ALTERNATIVES: I considered and rejected (a) SPEC-3's `lenny_adapter_leaked_slots` re-citation as an inventory gap, (b) §6.2:148's `slot_cleanup ──→ leaked` trigger annotation ("cleanup timeout exceeded") not covering the new "reclaim did not complete" trigger, (c) the reclaim hold's "accounted by the gateway as an ordinary transient slot failure" against spec/16:14's `maxConcurrentSessions > 1` scoping of `lenny_slot_failure_total`, (d) §15.4's mandated `ABORTED` needing a spec-side status-code inventory row. Each is refuted below.

FACT: there is NO adapter gRPC status-code inventory anywhere in spec/ or docs/reference/. `grep -rn "ABORTED\|FAILED_PRECONDITION\|INVALID_ARGUMENT" spec/ docs/reference/` returns only `docs/reference/metrics.md:284` (`lenny_upload_extraction_aborted_total`, unrelated). So §15.4's new "refused with the gRPC status code `ABORTED`" clause adds no row to any enumeration and creates no missing edit site. EVIDENCE: docs/reference/metrics.md:284.

FACT: §6.2's per-slot fence already diverges from §5.2 on the leak trigger BEFORE this proposal. spec/06_warm-pod-model.md:148 annotates the only edge into `leaked` as "cleanup timeout exceeded", while spec/05_runtime-registry-and-pool-model.md:545 says "If cleanup fails, the slot is leaked". The staged §7.1/§5.2 predicate ("a reclaim the adapter does not answer, or one answered `reclaimed` without reporting a clean exit") sits under §5.2's broader form, so it inherits a pre-existing mismatch rather than minting one. Do not file it as introduced by 0081.

FACT: §15.2's `RegisterAdapterUnderTest` "full matrix" (spec/15_external-api-surface.md:1440-1452) is about EXTERNAL PROTOCOL adapters (MCP, OpenAI Completions, Open Responses), not runtime adapters, so the new third-party runtime-adapter conformance obligations in §15.4 owe it no row. §15.4.6's suite is the runtime binary against a fake adapter (spec/15:2038-2079), exactly as the proposal's "deliberately untouched" list states. Two separate "conformance" surfaces; do not conflate them.

FACT: `Shutdown` appears nowhere in spec/28_communication-channels.md (`grep -n Shutdown` returns zero), confirming the "§28's registers" untouched claim. The §28 JSONL sequence at :888-906 step 8 is a Basic-level single-agent example rather than a co-tenanted concurrent pod, so the §29.4 step-13 co-tenancy sentence has no §28 sibling to mirror.

FACT: SPEC-3's added slot-cleanup actions check out against the sections they cite. The credential path `/run/lenny/slots/{sessionId}/credentials.json` is spec/04_system-components.md:1169's own spelling, and the same line is where the §4.9 direct-delivery-mode expiry timer and its `AUTH_EXPIRED` firing are defined; nothing there states when the timer is cancelled, so the added cancellation clause contradicts no §4.9 rule.

WATCHOUT: the natural operational finding here — "the pre-`running` cleanup's failure cannot reach `exited_cleanly`, because on that path no `Runtime.Close` runs and the shipped adapter discards `removeSlotTree`'s error" — is CLOSED by CODE-1, whose gate is `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`. Read Standing context #135 before chasing it; #67 alone (which records only the shipped `closeErr == nil` form) will send you down it. EVIDENCE: review-log.md:135 versus :67.

USEFUL [Standing context #357, #551, review-log.md:2063]: the recorded dead end that `lenny_adapter_leaked_slots` is gateway-emitted despite its name and despite §6.2:160 calling it adapter health metadata, plus the note that the gauge is in neither spec/16 nor docs/reference/metrics.md as a PRE-EXISTING hole. Those three entries are the whole reason this lens returns empty rather than filing two findings.

USEFUL [review-log.md:1091, :1023]: both record the SPEC-3 accounting-clause gaps (the unanswered-reclaim arm has no referent; the adapter's own `releaseSessionSlot` compensations are cleanups on that path with no `Shutdown`) as deliberately not filed. I reached both independently and agree they are under-specification rather than falsity, and the only available fixes are barred by the standing "no bind-path or start-state qualifier on §5.2's leak-accounting sentence" trap.


### [spec.7.review-performance.1]

DECISION: filed exactly one finding, on the reclaim hold's bound sentences rewritten this round — BECAUSE the r6→r7 diff is four hunks and three of them are wording; the one that changed a mechanism is the hold-bound rewrite at spec-changes.md:602, which replaced "the bound also covers the slot cleanup's own removal of the slot's directories" with "runs after that close under no deadline". That new sentence contradicts the retained §5.2 `**Slot cleanup:**` bullet (spec/05:545, "Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter)") over the very acts SPEC-3 is widening that bullet to name. ALTERNATIVES: filing the hold-refusal-drives-pod-drain accounting (dropped, see below) and filing the epoch ABA across an adapter process restart (dropped, see UNVERIFIED).

MISTAKE: the r7 fix went one step past the round-6 refutation. The refuted finding said the hold's stated bound did not cover the tree removal; the refutation's own ground was that "the cleanup ends in either outcome at or before the per-slot timeout". The fixer answered by declaring the removal deadline-free, which is the reading the refutation rejected, and it now collides with the bullet the same block says stands verbatim (spec-changes.md:618-620).

FACT: the whole-pod replacement trigger's own enumeration of what counts is at spec/05:561 and `leaked` is glossed there and at spec/06:148 as "cleanup timeout exceeded". A deadline-free cleanup act makes that terminal unreachable for a hung removal and makes the hold permanent. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545,:561; spec/06_warm-pod-model.md:148.

DECISION: did NOT file "a hold refusal is accounted as an ordinary transient slot failure, and `UnhealthyThreshold(2) == 1`, so one refusal drains a healthy pod" — BECAUSE the drain is almost unreachable on the paths that can meet the hold. An ACKNOWLEDGED compensation has finished its tree removal before the gateway returns the error to the client (standing Settled entry on `compensateFailedSlotBind` blocking on `cl.Shutdown`), so the client's §15.1 retry on a create-time-reserved slot never meets the hold; an UNACKNOWLEDGED one is already `leaked`, which drains the pod at concurrency 2 on its own; and the §7.4 mid-session upload builds no `SlotBindError` and is counted nowhere (non-spec-changes.md:1725-1729). Only a client-driven §7.3 resume onto the reclaiming pod remains, and the proposal prices that one explicitly at non-spec-changes.md:1723-1725. EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2871 (`RecordFailure` → `Unhealthy` → `DrainSandbox`).

UNVERIFIED: the bind epoch can ABA across an adapter PROCESS restart, and nobody has checked it. `adapterclient.Dial` uses `grpc.NewClient` (pkg/gateway/runtime/adapterclient/client.go:49-53), so "the connection the failed attempt already holds" is an auto-reconnecting `grpc.ClientConn`, not a process instance. The staged caller rule grounds the fence on that connection ("meaningless without it", spec-changes.md:~697), and the epoch restarts from its base in a fresh adapter process, so a lagging compensation carrying epoch 1 could compare equal against a restarted adapter's first entry for the same session. I judged it unreachable in practice because grpc-go fails a non-`WaitForReady` RPC when the channel enters TransientFailure, so the compensation errors out within one backoff rather than waiting for the retry to bind. Somebody with a code-lane brief should confirm that reasoning rather than trusting it.

FACT: "a hold taken outside any request" in the staged §5.2 paragraph is grounded — `deregisterStartedSessions` (pkg/adapter/slotsession.go:375-395) is the pod-shutdown pass that deregisters entries with no RPC in hand. Do not file that clause as vacuous.

USEFUL [Standing context, entry on the amendment adding no control-plane or data-plane write]: correct and it saved the whole capacity half of this lens. The epoch is an in-memory `int64` and the hold an in-memory map key; there is no new etcd write, Postgres row, Redis key, informer or metric series, so there is no write-amplification, hot-key or informer-memory math to do. A future performance lens on this proposal should spend its time on the fail-closed gate's bounds and on the health accounting instead.


### [spec.8.review-applicability.1]

DECISION: returned an empty findings list again — BECAUSE the spec staging is byte-identical to the r7 snapshot (`diff -u scratchpad/cp-snap/.../spec-r7/0081_....spec-changes.md` against live returns nothing, and the same holds for every other proposal file except the review log), and every mechanical check this lens owns re-ran clean on that unchanged text — ALTERNATIVES: re-deriving the mechanism arguments in the refuted list (rejected: they are other lenses' and already adjudicated), and filing checklist/Depends-on drift (rejected: the brief puts checklist reconciliation outside this loop).

FACT: round 7 produced NO edit to any proposal file. Only the review log changed between the r7 and r8 snapshots, so the r8 snapshot equals the live proposal and `diff -ru .../spec-r8 proposals/0081_...` is empty. Diff against `spec-r7` (or earlier) to see any delta. EVIDENCE: scratchpad/cp-snap/0081_.../spec-r7 vs proposals/0081_...

FACT: the whole mechanical sweep is reproducible in one python block and takes under a minute. Parse the fenced blocks after `## Staged edits` (line 270 of the spec-changes file); there are 31. Blocks 0,2,5,8,10,12,14,16,18,20,22,24 are the anchors and each occurs EXACTLY ONCE in `spec/*.md`; every other block occurs zero times (they are insertions/replacements). Anchor targets, all distinct, no two edits landing on one line: 04:157, 04:686, 07:23, 07:210, 07:213, 07:214, 07:414, 06:234, 05:555, 04:854, 05:545, 05:453. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:270 onward.

FACT: every markdown link inside the staged blocks resolves to a real heading anchor in the target spec file, intra-file links included (checked by deriving anchors from `^#{2,6}` headings across spec/*.md). Named insertion points all exist and are unique in their scope: `*Adapter → Gateway RPCs:*` at spec/04_system-components.md:688 with `#### 4.7.2` at :695; `**SDK-warm demotion contract:**` at spec/15_external-api-surface.md:1469 with `#### 15.4.1` at :1471; the §6.2 fence heading `Per-slot sub-states (tracked per session, not as pod-level phase; a pod of either concurrency):` at spec/06_warm-pod-model.md:150 with the fence closing at :157 and `**`reserved` hold semantics.**` at :158.

WATCHOUT: spec/06_warm-pod-model.md has TWO near-identical per-slot sub-state heading lines inside the same fence — :146 `Per-slot sub-states scoped to concurrent occupancy (tracked per session):` and :150 `Per-slot sub-states (tracked per session, not as pod-level phase; a pod of either concurrency):`. SPEC-4 quotes :150 verbatim, so it is unambiguous, but a paraphrased anchor here would be. EVIDENCE: spec/06_warm-pod-model.md:146,150

WATCHOUT: SPEC-1's §29.4 append anchors on the sentence ending `([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).`, which occurs FOUR times in spec/29_communication-scenarios.md (:645, :711, :887, :982). Only the §29.4-step-13 scoping in the instruction disambiguates it; :711 is the intended site. EVIDENCE: spec/29_communication-scenarios.md:711

FACT: the N3 reserved-phrase sweep (`lifecycle channel(s)`/`control channel(s)`, both spellings, case-insensitive) and the line-citation sweep (`spec/NN_*.md:NNN`, `§X.Y:NNN`, `line NNN`) over the staged blocks return nothing, so neither the naming lint nor the citation ratchet fires on the applied text. Note the spec-changes file DOES carry line citations in its surrounding rationale prose (e.g. `spec/29_communication-scenarios.md:586-588`), but `proposals/` is outside both prohibitions' domain.

FACT: no checklist box is ticked (`grep '^\s*- \[x\]'` on the implementation checklist returns nothing) and no staged spec text references the deleted open decision 9 or the nonexistent "D1 Option B".


### [spec.8.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every verbatim anchor, every file:line citation and every markdown anchor in the spec staging resolves correctly against the tree, and the two drifts I found are explicitly below the bar. ALTERNATIVES: filing the two drifts (see MISTAKE-adjacent notes below); rejected because neither changes the meaning of the claim it supports.

FACT: the snapshot the round-8 orchestrator points at is IDENTICAL to the live proposal — `diff -rq scratchpad/cp-snap/.../spec-r8 proposals/0081_...` returns nothing. The "read the changed sections first" instruction had nothing to bite on this round; treat the whole document as equally aged.

FACT: every verbatim "The text to replace reads" anchor in the spec staging is byte-exact against the tree as of this round. Verified: spec/04_system-components.md:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening), :854 (§4.7.9 step 5); spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**` pod-selection sentence); spec/06_warm-pod-model.md:150-156 (per-slot fence, insertion point before the `reserved` hold paragraph at :158), :234 (mid-resume cancel clause); spec/07_session-lifecycle.md:23 (atomicity parenthetical, and the paragraph really does sit between the step-8 line at :21 and the continuing line at :24), :210 (§7.2 preamble premise), :213 (step 2 tail), :214 (step 3), :414 (§7.3 list item 4); spec/29_communication-scenarios.md:711 (step 13 tail).

FACT: every markdown anchor the staged text mints resolves to a real heading, and each matches the slug spelling the spec already uses elsewhere (`#471-role-and-gateway-rpc-contract` at spec/README.md:28, `#1542-rpc-lifecycle-state-machine` at spec/15:2017, `#479-startup-sequence-for-type-agent-runtimes` at spec/README.md:36). Both SPEC-5 insertion points exist and are unambiguous: spec/04:688 (`*Adapter → Gateway RPCs:*` table) closing before `#### 4.7.2` at :695, and spec/15:1469 (`**SDK-warm demotion contract:**`) before `#### 15.4.1` at :1471.

FACT: the code-side attributions the spec staging leans on are all true of the tree. `slotlayout.RemoveTree` removes `CredentialsDir` (pkg/adapter/slotlayout/tree.go:59-70) and `deregisterSlotLocked` cancels every armed expiry timer (pkg/adapter/slotsession.go:174-182), so SPEC-3's "both added actions are shipped behaviour" holds. `DemoteSDK` really does remove the entry (pkg/adapter/sdkwarm.go:296-298 calls `releaseSessionSlot`), which is what §4.7.1's caller rule rests on. `stageWorkspace` sends `PrepareWorkspace` only under `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1322). `Binder.ReleaseSlot` sends the unfenced `Shutdown` (slotbinder.go:542) and the recycle one (:575). `ReportSessionScrub` increments `sessionsServed` (pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:44). `PrepareWorkspace` is genuinely client-streaming (schemas/lenny-adapter.proto:41). `ShutdownRequest.coordination_generation = 6` sits at schemas/lenny-adapter.proto:1630-1635 exactly as SPEC-1 cites.

WATCHOUT: two sub-threshold drifts, recorded so a later lens does not spend a round re-deriving them and then filing them. (1) spec-changes.md:355 cites `spec/29_communication-scenarios.md:697` for step 12's "the adapter closes the session runtime"; that string is on :698 (:697 is the sentence before it). Off-by-one, meaning unchanged. (2) spec-changes.md:~336 says §7.1's atomicity paragraph "closes by stating that the client never receives a `session_id`..."; that sentence is mid-paragraph and the paragraph actually ends "...regardless of the flag." (which the SAME document quotes correctly at :348). Neither changes the argument either sentence supports.

USEFUL [standing-context: MISTAKE, refuted at least eight times — "§7.1 says the exclusive pod retires while §6.2:283 and §7.2 step 3 release it back to the pool"]: I re-derived this from cold (spec/06_warm-pod-model.md:283 "The pod is marked `failed` and released back to the pool" versus the staged "the pod retires") and was minutes from filing it before the log entry stopped me. The reconciliation is spec/06:80 and :95: a terminal claim disposition (`released` OR `failed`) projects `draining` and then `terminated`. Anyone reading :283 alone will rebuild this finding; the standing-context entry is what keeps saving the round.


### [spec.8.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens on the spec staging — BECAUSE every client-facing parallel of the amendment checks out against the tree (see FACTs below) — ALTERNATIVES: filing the §7.3-resume-classification and docs-mirror items, rejected because both remedies land outside the staged spec edits (code / DOCS deliverables), which this loop may not close.

FACT: the spec-changes staging has not moved since round 7. `diff -rq scratchpad/cp-snap/0081.../spec-r8 proposals/0081...` is empty, and `spec-r7` differs only in the review log. So r8 had no fix-stage delta to read hardest. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r8

FACT: every SCHEMA-1 field number is genuinely free on its message, checked mechanically. `PrepareWorkspaceResponse` holds 1,2 (new 3); `FinalizeWorkspaceResponse`, `RunSetupResponse`, `StartSessionResponse`, `ConfigureWorkspaceResponse` hold 1 (new 2); `ResumeResponse` holds 1,2,3 (new 4); `AssignCredentialsResponse` is `{}` (new 1); `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved as `slot_id` (new 7); `ShutdownResponse` holds 1,2 (new 3). Nine fields, matching §15.4's staged "seven bind-sequence responses and the fence and its outcome". EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1433,1690,1033,1609-1636,1665

FACT: §28 carries no `Shutdown` row and no per-field register, so the "§28's registers untouched" claim is sound. `grep -n Shutdown spec/28_communication-channels.md` returns nothing, and the channel cards' `**Messages.**` bullets name messages rather than fields. EVIDENCE: spec/28_communication-channels.md:386-389

FACT: the proto ALREADY publishes "a pod validates the generation on every gateway-to-pod RPC", so SPEC-5's "Where both appear on one message, as on `Shutdown`, each is checked on its own terms" is consistent with the published wire contract even though the shipped adapter validates `coordination_generation` only in `CoordinatorFence`/`CheckpointBarrier`. Do not file that sentence as a false attribution; the divergence is the pre-existing UNWIRED R16 claim-register deferral. EVIDENCE: schemas/lenny-adapter.proto:1630-1635

FACT: the hold refusal mints no client-visible value on any of its three client-reachable exits. The bind path lands on the existing `transient` slot-failure category (pkg/gateway/podlifecycle/podsession/slotfailure.go:36), the §7.4 mid-session upload lands on the existing `502 UPSTREAM_ERROR` (pkg/gateway/sessionserver/upload_to_session.go:128-137, catalogued at spec/15_external-api-surface.md:1028), and `WARM_POOL_EXHAUSTED{concurrent_slots_exhausted}` is reused verbatim (spec/05_runtime-registry-and-pool-model.md:549). §15.1's "deliberately untouched" row holds.

FACT: §15.4.6's conformance battery really does run the runtime binary over JSONL against a *fake adapter*, so it can observe no adapter obligation and CONF-1 correctly lands in tier 10 instead. EVIDENCE: spec/15_external-api-surface.md:2038-2048

WATCHOUT: `DemoteSDK` genuinely deregisters the entry and runs the per-slot release synchronously inside its own handler, which is what makes SPEC-3's "a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" true for the SDK-demotion fallback. Verify it in code before re-opening that sentence. EVIDENCE: pkg/adapter/sdkwarm.go:296-301

DEFERRED [proposals/0081.../0081....non-spec-changes.md]: no DOCS deliverable covers `docs/reference/adapter-contract.md`, whose `Shutdown` description is the docs mirror of the §4.7 row SPEC-1 rewrites and of the epoch/outcome fields SCHEMA-1 adds. What is true instead: after SPEC-1 and SPEC-5 apply, that page states one teardown with one precondition and no reclaim outcome. Earlier rounds established that no tier-11 gate catches the drift (`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts four substrings that all survive), so nothing will fail. The non-spec loop owns adding the edit site.

OPEN: a client-driven §7.3 resume refused by the reclaim hold reaches `holdOrFailOnResumeError`, whose `isTransientPodClaimError` is a CLOSED enumeration of sentinel types (pkg/gateway/sessionserver/start.go:3648-3680) that a bare `codes.Aborted` from the adapter matches on none of. It therefore falls through to `s.failSession`, taking the row terminal, which contradicts the staged "the refusal is transient, so the attempt keeps its §5.2 retryability" for that one caller. I did not file it: the reachability is doubtful (a §7.3 re-attach claims idle inventory and lands on a *replacement* pod, and the hold on the original pod ends inside the `Shutdown` RPC), and any remedy is a CODE deliverable. The non-spec loop should decide whether CODE-4's `*SlotBindError` fold needs a matching `isTransientPodClaimError` arm.


### [spec.8.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE the docs-alignment lens has almost no
remedy space left inside a spec-only loop, and every avenue I could still reach resolved to
already-refuted ground or to a docs edit the next loop owns — ALTERNATIVES: I considered
filing (a) the `lenny_adapter_leaked_slots` gauge SPEC-3 newly names in §5.2, (b) the
`concurrent_slots_exhausted` reason the new placement exclusion now produces for a pod that is
not full, and (c) `ABORTED` entering `spec/` for the first time; each died on verification,
reasons below.

FACT: the r8 snapshot is byte-identical to the current proposal directory.
`diff -rq scratchpad/cp-snap/.../spec-r8 proposals/0081_...` returns nothing, so round 7
produced no fix and the "read the changed sections hardest" instruction has no target this
round. Do not spend a tool call re-running it expecting hunks.

FACT: `lenny_adapter_leaked_slots` is ALREADY named in `spec/` twice before this proposal —
spec/05_runtime-registry-and-pool-model.md:545 (the `**Slot cleanup:**` bullet's closing
sentence) and spec/06_warm-pod-model.md:160 (`**leaked** slot semantics`, which also fixes its
`pod_id, pool` labels). SPEC-3's staged mention adds no new metric surface and therefore owes
no spec/16 or docs/reference/metrics.md companion. This confirms standing-context entry
"The `lenny_adapter_leaked_slots` gauge is wired in production…"; the gauge's absence from
spec/16 is pre-existing and is not something 0081 introduces.

FACT: `ABORTED` occurs ZERO times across `spec/`, `docs/` and `schemas/`
(`grep -rn 'ABORTED' spec/ docs/ schemas/` is empty), so SPEC-5's §15.4 reclaim-hold block is
the first gRPC status code the specification names on the adapter surface. I did not file it:
the shipped adapter already returns `ABORTED` (standing-context trap "Do NOT file the
reason-value or status-code tables in `docs/`", citing docs/api/internal.md:490-500), so the
docs table's omission is pre-existing, and its only remedy is a docs edit this loop may not
land.

WATCHOUT: the `concurrent_slots_exhausted` angle looks live and is not. spec/05:549 defines
that `details.reason` as "pods exist but all slots are full", while the SPEC-2 placement
exclusion makes a NOT-full pod produce it. But `ClaimSlot` already returns
`ErrNoConcurrentSlot` for every non-tenant-mismatch candidate it skips
(pkg/gateway/podlifecycle/podclaim/slotclaimer.go:505-523), so the reason is already broader
than its §5.2 definition in the shipped tree. This is the same class as the refuted
`WARM_POOL_EXHAUSTED`/troubleshooting-table candidate: one more producer of a pre-existing
enumeration gap. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:549;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:514-523.

FACT (verified, so nobody re-derives them): the "Spec sections deliberately untouched" claims
hold. `Shutdown` appears nowhere in spec/28 (`grep -n 'Shutdown' spec/28_...` empty), so the
§28-registers claim is true. §15.4.6's category table is a runtime-binary-over-JSONL battery
run against a fake adapter (spec/15_external-api-surface.md:2038-2075), so it genuinely cannot
observe an adapter obligation and CONF-1 belongs in tier 10; `tests/tier10_conformance/`
already carries adapter-contract tests, so that landing site is real.

FACT: every markdown anchor the staged spec text writes resolves to a real heading —
spec/04:657 §4.7, :659 §4.7.1, :848 §4.7.9, :1099 §4.9; spec/05:365 §5.2; spec/06:78 §6.2;
spec/07:3 §7.1, :378 §7.3, :438 §7.4; spec/10:3 §10.1; spec/15:614 §15.1, :1458 §15.4,
:1686 §15.4.2. The SPEC-5 insertion point is also real: the `*Adapter → Gateway RPCs:*` table
ends at spec/04:693 and `#### 4.7.2` is at :695.

FACT: staged §4.7.1's caller rule "`DemoteSDK` … removes the entry" is true of the tree —
`DemoteSDK` calls `s.releaseSessionSlot(sessionID)` at pkg/adapter/sdkwarm.go:298.

USEFUL [standing context, DEFERRED docs/reference/adapter-contract.md:75]: it told me the one
docs site eleven lenses have filed is already owned by the non-spec loop, which stopped me
re-deriving it and re-filing it out of scope. It is still the single most valuable entry in the
log for a docs lens.


### [spec.8.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens — BECAUSE every identifier
the staged spec text mints or changes was greped across `spec/`, `docs/`, `schemas/` and
`charts/` and each landed either on a site the proposal already stages or on nothing at all.
ALTERNATIVES: I built and dropped three candidates (the reclaim hold's wide
"create or resolve a registry entry" predicate reaching `Attach`/`Interrupt`/`Checkpoint`/
`CoordinatorFence`; the §4.7.1 sentence "Where both appear on one message, as on `Shutdown`,
each is checked on its own terms" asserting a generation check the shipped adapter does not
perform; and `spec/18` phase 12c against the four new deliverables). Each is either
pre-existing, already barred by a standing trap, or spec-internally consistent.

FACT: the staged-edit text has not moved since `spec-r7`. `diff -rq` over the whole
`scratchpad/cp-snap/0081_.../` tree shows `spec-r7`, `spec-r7-start`, `spec-r8-start` and
`spec-r8` all byte-identical to the live `.spec-changes.md`; the newest snapshot that
differs is `spec-r6`. The r6→now delta is exactly six hunks and is small: §7.1 and the
Design lose "spends one of the attempts the retry policy allows" and gain "an attempt that
meets the hold is one that policy did not place"; the §5.2 hold paragraph loses "This is the
only rule under which the adapter refuses a request on the bind path" and re-splits its
bound sentence; §4.7.1's bind-sequence paragraph and §15.4's second clause re-point the
refusal at the reclaim hold; §4.7.1's caller rule is rewritten so a `Shutdown` carries an
epoch only when it compensates an abandoned attempt; and two edge-case bullets stop citing
"open decision D1's Option B". EVIDENCE: `diff -U6 scratchpad/cp-snap/0081_.../spec-r6/*.spec-changes.md proposals/0081_.../*.spec-changes.md`

FACT: the new-identifier sweep is clean and here is the command set that produces it.
`grep -rn "bind epoch\|bind_epoch\|BindEpoch\|superseded\|SLOT_RECLAIM" spec/ docs/ schemas/ charts/`
returns only unrelated `superseded` uses (checkpoint partial manifests, ADR front matter,
§28's enumeration rule, admin token rotation) — no pre-existing bind-epoch surface anywhere.
`grep -rn "per-session teardown\|per-slot teardown\|whole-pod teardown\|runtime teardown\|reclaim hold\|bind sequence" spec/ docs/ schemas/ charts/`
returns exactly one line, `spec/04_system-components.md:157`, which SPEC-1 replaces, so the
vocabulary SPEC-1 retires has no second home and the terms it mints collide with nothing.
"slot release" matches only the `slot_cleanup ──→ released` annotation gloss at
spec/06:155 and its docs mirror at docs/reference/state-machines.md:237, both barred by a
standing trap. This extends Settled #231 to the amendment's identifiers.

FACT: the per-slot sub-state names have exactly the sites Settled #243 records, re-verified.
`grep -rn "per-slot sub-state\|receiving_uploads\|slot_cleanup\|slot_assigned" spec/ docs/`
gives spec/06:148,151-155 (the fence), spec/06:175-177 (the "Concurrent pod lifecycle"
paragraph, which points at the fence and enumerates no edges), spec/07:162 (a pointer note),
spec/15:672 (the internal-only-states sentence, which names coarse POD phases only), and
docs/reference/state-machines.md:234-237,251 (DOCS-1's target). No spec/ site other than the
fence enumerates a per-slot edge, so SPEC-4's added edge has no unstaged spec mirror.

FACT: both SPEC-5 insertion points and every RPC the epoch block names check out against the
tree. `#### 4.7.1 Role and Gateway RPC Contract` is spec/04:659, the Adapter → Gateway table
closes at :693 and `#### 4.7.2` opens at :695; `**SDK-warm demotion contract:**` is
spec/15:1469 and `#### 15.4.1` is :1471. All seven bind-sequence RPCs have unary responses in
`schemas/lenny-adapter.proto` (:41,:48,:55,:62,:72,:89,:151), so "reports the entry's current
epoch on its response" is expressible for each, and `PrepareWorkspace` is the only
client-streaming one, which is the case §4.7.1 carves out by name.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule
("`DemoteSDK` … removes the entry") is not a mis-attribution. `Server.DemoteSDK` calls
`s.anyRegisteredSession()` and then `noteRuntimeClosed` + `releaseSessionSlot`.
EVIDENCE: pkg/adapter/sdkwarm.go:274-302,:309-316

FACT: `Shutdown` is on no §28 channel, confirming the "deliberately untouched" §28 bullet.
The five `gateway-to-pod` channels on `LNK-POD-GRPC` are CH-ATTACH, CH-CHECKPOINT, CH-FENCE,
CH-BARRIER and CH-PODHEALTH. EVIDENCE: spec/28_communication-channels.md:118-122

FACT: `spec/18` still needs no edit after the amendment, and the ground is wider than
Settled #274 recorded. Phase 12c's deliverables (spec/18:531-533) enumerate no adapter
precondition, and the only conformance deliverable in spec/18 is Phase 5's
`cmd/lenny-compliance` "Basic-level battery" at :122, which exercises the RUNTIME BINARY over
JSONL. CONF-1 lands in `tests/tier10_conformance`, not there, so SPEC-5's third-party adapter
obligation orphans no phase deliverable.

WATCHOUT: `tests/tier11_docs/spec_47_rpc_row_naming_test.go` reads §4.7 with
`specSection(..., "### 4.7 ")` and harvests every line matching
`^\|\s*` + backticked-name + `\s*\|` as an RPC row name, then holds every
`§4.7 table names <RPC>` Go comment to that set. SPEC-5 inserts prose (no table) inside §4.7,
so it is green today — but any future §4.7 or §4.7.1 edit that introduces a markdown TABLE
whose first column is a backticked identifier silently widens that gate's accepted row set.
EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:32-53

UNVERIFIED: the reclaim hold's predicate is "any request that would create or resolve a
registry entry", which Settled #318 shows reaches `Attach`, `Interrupt`, `SendMessage`,
`Checkpoint`, `CheckpointBarrier`, `CoordinatorFence`, `ExportPaths`, `ReportUsage` and the
three credential RPCs — not only the bind sequence. §15.4 turns that into a normative
`ABORTED` on the wire. I did not file it: the refuter of the "§15.4 exempts `Shutdown`"
finding already read "create or resolve" as the change's own vocabulary for the
bind-sequence RPCs, so a breadth finding lands on the same refutation. If a later round wants
it, the correction is to narrow §5.2's and §15.4's predicate to the bind-sequence RPCs the
§4.7.1 block already enumerates by name, and the argument has to start from §4.7.2's
Checkpoint/Interrupt operation lock (which QUEUES rather than refuses) rather than from the
bind path.

UNVERIFIED: §4.7.1's "Where both appear on one message, as on `Shutdown`, each is checked on
its own terms" newly asserts in spec text that `coordination_generation` is validated on
`Shutdown`. That agrees with spec/10:41 ("The pod accepts only RPCs whose generation
matches") and disagrees with the tree (Settled #64: only `CoordinatorFence` and
`CheckpointBarrier` validate it). The applied spec stays self-consistent, so I did not file
it, but it makes standing Settled #337's hazard ("if `Shutdown` ever starts enforcing the
gate, a fenced attempt's §7.1 compensation becomes unsendable") a spec-stated obligation
rather than a code-lane possibility. A conformance lens after implementation should decide
whether CODE-6 owes the generation check on `Shutdown` or whether the sentence should say
the generation is not enforced there.


### [spec.8.review-feasibility.1]

DECISION: returned an EMPTY findings list — BECAUSE every actor-action assignment in the staged
spec resolves against the tree, and the three angles I had that were not already in Standing
context died on evidence — ALTERNATIVES: rejected filing the §28.3 `LNK-POD-GRPC` multiplicity
angle (already killed, see below), the DemoteSDK-timeout angle (killed by §4.7:673's own failure
branch), and the §7.2 step-3 two-actor ordering angle (Standing context 87/89/91 already worked it).

FACT: the r6 snapshot is the newest one that DIFFERS from the live proposal; `spec-r7`, `spec-r7-start`,
`spec-r8` and `spec-r8-start` are all byte-identical to it. `diff -u spec-r6/... proposals/...` is a
~10-hunk delta and is the whole readable r6→r8 change set. EVIDENCE: scratchpad/cp-snap/0081_.../spec-r6

FACT: every actor the staged spec names can perform what it is assigned, verified against the tree:
all seven bind-sequence RPCs are rows in §4.7's Gateway→Adapter table (spec/04:669-684) and RPCs in
the proto (schemas/lenny-adapter.proto:41,48,55,62,72,89,151); `PrepareWorkspace` resolves the slot
exactly once, at the first frame that carries a session id, so §4.7.1's client-streaming clause is
implementable as written (pkg/adapter/staging.go:75-83); `RunSetup` reaches `ensureSlotPaths` so it
can resolve or create an entry (staging.go:337); `DemoteSDK` does remove the entry inside its own RPC
(pkg/adapter/sdkwarm.go:296-298 `noteRuntimeClosed` + `releaseSessionSlot`), which is what makes both
§4.7.1's caller rule and §5.2's "a release that runs its cleanup inside the RPC that requested it"
true; `deregisterSlotLocked` cancels every armed §4.9 expiry timer, so SPEC-3's added action-list
clause is shipped behaviour (pkg/adapter/slotsession.go:174-181).

FACT: "the slot identifier is the session identifier" is not only true in code
(`SlotID: req.SessionID`, pkg/gateway/podlifecycle/podclaim/slotclaimer.go:760) — the PROTO says it
too, in the `reserved 4` comment on `ShutdownRequest`: "a session-mode slot's identifier is its
session's identifier". EVIDENCE: schemas/lenny-adapter.proto:1613-1619. Anyone asked to substantiate
that sentence should cite the proto comment rather than hunting spec/ prose, which does not state it.

FACT: `ShutdownRequest.coordination_generation = 6` is a bare non-optional `int64`
(schemas/lenny-adapter.proto:1630-1635), which is exactly what SPEC-1's §4.1 rationale claims, and the
SHIPPED adapter validates that field on `CoordinatorFence` and `CheckpointBarrier` ONLY
(pkg/adapter/coordination.go:120,:262 are the only `GetCoordinationGeneration` reads outside tests).
So the fenced compensation cannot be rejected on a generation mismatch today. The §4.1/§10.1 "a pod
validates the generation on every gateway-to-pod RPC" claim is a pre-existing spec/code divergence
and is NOT this proposal's to fix.

WATCHOUT: the DemoteSDK-timeout angle looks like a live finding and is not. §5.2's hold paragraph
asserts unconditionally that "the pod-warm bind sequence that follows an SDK demotion does not meet"
the hold; that would be false if the gateway's 5s `DemoteSDK` deadline could expire while the
adapter's cleanup still ran, since the fallback bind would then hit a live hold. It cannot: §4.7's
`ConfigureWorkspace` row routes a FAILED `DemoteSDK` to "the pod transitions to `failed` and a
replacement is claimed", so the fallback materialization runs only after a successful return.
EVIDENCE: spec/04_system-components.md:673.

MISTAKE (avoided, and already recorded once): the §28.3 `LNK-POD-GRPC` row says "One connection per
gateway replica per pod" (spec/28_communication-channels.md:106), which reads as making §4.7.1's
per-connection epoch latch share one connection across co-tenant sessions and so name a co-tenant's
epoch. It dies twice over: production dials a fresh client per bind (`b.DialAdapter` inside
`connect`, pkg/gateway/podlifecycle/podsession/binder.go:1776, and inside `connectSlot`,
slotbinder.go:459-460), and the bind RPCs sit on no §28 channel register at all. Two earlier
feasibility rounds already chased and dropped it. EVIDENCE: review-log.md:2617-2622 and the
`spec.6.review-feasibility.1` FACT.

USEFUL [Standing context, Settled 72/73/76/91]: the sixteen-site anchor sweep, the anchor-resolution
list, the three-start-RPC entry and the mid-resume-connection entry each saved a full re-derivation.
I re-checked only the anchors my own angles touched (spec/04:659,665,688,695 for the §4.7.1 insertion
point; spec/15:1469,1471 for the §15.4 one; spec/29:706-711 for step 13) and all held.

FACT: §15.4.6's conformance suite really does exercise the runtime binary over JSONL against a FAKE
adapter, with no adapter under test, so the "deliberately untouched" justification for it is accurate
and CONF-1's move to tier 10 is right. tier10_conformance exists and already carries adapter-contract
cases. EVIDENCE: spec/15_external-api-surface.md:2038-2050; tests/tier10_conformance/adapter_contract_event_taxonomy_test.go


### [spec.8.review-fresh.1]

FACT: the r8 snapshot at scratchpad/cp-snap/.../spec-r8 is BYTE-IDENTICAL to the live proposal directory (and so is spec-r8-start), so the "read what changed first" instruction had nothing to point at this round. `diff -rq` returned empty. Do not spend time re-running it if a future round sees the same. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r8/

FACT: every "text to replace" anchor in the staged edits still matches the tree verbatim and uniquely. Re-verified this round: spec/04:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening), :854 (§4.7.9 step 5); spec/05:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**`); spec/06:152-153 (`receiving_uploads ──→ running`), :234 (`resuming → cancelled`); spec/07:23, :210, :213, :214, :414; spec/29:711 (step 13, scoped by "In §29.4's numbered step 13" because the same string closes step 6 at :645). Insertion landmarks also verified: `*Adapter → Gateway RPCs:*` table closes at spec/04:694 with `#### 4.7.2` at :695; `**SDK-warm demotion contract:**` at spec/15:1469 with `#### 15.4.1` at :1471; `**\`reserved\` hold semantics.**` follows the §6.2 fence close.

FACT: the §29.4 step-13 addition's premise checks out. The CH-RUNTIMEOPS `terminate` frame schema is `type`, `deadlineMs`, `reason` and carries no session field, so "pod-global and names no session" is true. — EVIDENCE: spec/28_communication-channels.md:1082

FACT: §5.2's `**Whole-pod replacement trigger:**` is at spec/05:561 and the `**Slot cleanup:**` bullet at :545, both BELOW the `**Scrub model.**` paragraph at :453, so SPEC-3's appended "the **Slot cleanup:** bullet below" and "the whole-pod replacement trigger stated below" both resolve in the right direction.

FACT: the bind RPCs are explicitly OFF the §28 register. §29.4 step 12 labels the `Shutdown` call "`gateway` → `adapter`, no register entry, the internal control API ([§15.3])". So §28.3's `LNK-POD-GRPC` "One connection per gateway replica per pod" multiplicity is not obviously normative for the bind/teardown RPCs, which is why the r7 reviewer's UNVERIFIED on the two-connection claim was left unfiled. — EVIDENCE: spec/29_communication-scenarios.md:692-694; spec/28_communication-channels.md:106; review-log.md:2617-2622

DECISION: I filed ONE finding, on §4.7.1's caller rule granularity (per connection versus per session), and nothing else. BECAUSE the caller-holding rule at spec-changes.md:682 defines a single per-connection value ("the most recent epoch a response reported to it on one adapter connection") while the naming rule two sentences later reads it per session ("the epoch the caller holds **for that session**"), which is a predicate drift inside the newest, least-examined block. ALTERNATIVES rejected: (a) filing the §28.3 multiplicity contradiction on its own — the r7 reviewer already looked at it and the §29.4 "no register entry" label defeats it; (b) filing "the generation names the gateway replica that speaks for the session" (spec-changes.md:678) as a false §10.1 citation — §10.1:30 makes the generation a per-takeover counter rather than a replica name, but each generation value corresponds to at most one coordinator tenure, so the gloss is defensible and it is wording; (c) filing the §7.1-inside-a-fenced-code-block markdown hazard — the atomicity paragraph at spec/07:23 already sits inside the ``` fence spanning :5-54 and already carries markdown links, so the new paragraph matches a PRE-EXISTING condition and is not this proposal's defect.

WATCHOUT: the §4.7 row's "A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response" looks like it breaks the occupancy-zero recycle `Shutdown`, which must still run the whole-pod scrub. It does not: the sentence is scoped to "removes nothing, runs neither teardown", the scrub is neither teardown, and SPEC-1's §4.1 replacement keeps "runs the whole-pod scrub when the recycle disposition is set". Do not re-file this. — EVIDENCE: spec-changes.md:285, :322; spec/05_runtime-registry-and-pool-model.md:459

WATCHOUT: production dials a FRESH adapter client per connect (`Binder.connect` at pkg/gateway/podlifecycle/podsession/binder.go:1776 and `dialSandbox` at :1152, both via `b.DialAdapter`), so the code's connection is per bind attempt and the per-connection epoch latch works there. The spec-side hazard is that nothing in the staged text or in spec/ requires that. Do not use the code to conclude the spec text is sound.


### [spec.8.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on the spec staging, the fourth consecutive empty return for this lens on this proposal (rounds 3, 5, 6, 7 and now 8) — BECAUSE the round-7 fix stage's six hunks are all prose on the gateway-adapter gRPC surface and none of them adds, moves or qualifies a Kubernetes surface — ALTERNATIVES: I re-derived the three standing candidates (missing level-triggered backstop behind the edge-triggered reclaim, the widened `leaked` class as a stuck-object footgun, the §4.1 `DeleteOptions`-precondition analogy) and each is closed by text already in the tree; filing any of them is re-litigation.

FACT: the round-8 delta is NOT empty against the snapshot the brief names, but it is empty against what my lens last read. `diff -rq scratchpad/cp-snap/0081_.../spec-r8` and `spec-r7` against the live proposal both return only `review-log.md`; the newest snapshot whose `.spec-changes.md` differs is `spec-r6`. So the six real hunks were written in round 7's FIX stage, after `[spec.7.review-kubernetes.1]` ran — that shard's WATCHOUT saying "there is none" was true when it was written and is misleading now. Diff against `spec-r6`, not `spec-r7`. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r6/0081_...spec-changes.md

FACT: the six round-7 fix hunks, for a lens deciding whether to read them: (1) Design §7.1 retry sentence re-worded from "spends one of the attempts the retry policy allows" to "an attempt that meets the hold is one the retry policy did not place" (spec-changes.md:61-62); (2) the Design sentence "the hold is the only rule that refuses a request on the bind path" DELETED (:148); (3) two edge-case bullets re-pointed off the non-existent "open decision D1's Option B" onto the accepted-failure-mode list (:188, :244-247); (4) the same retry re-wording inside the staged §7.1 paragraph (:390); (5) the §5.2 reclaim-hold bound split into a close-bounded clause plus a deadline-free directory removal (:602); (6) §4.7.1's caller rule re-stated as "A `Shutdown` carries an epoch only when it compensates a bind attempt the caller abandoned" and two exhaustiveness clauses softened (:680, :682, :704). None touches a CRD, a status subresource, a finalizer, an informer, an admission webhook, RBAC, a NetworkPolicy, a chart or a podspec.

FACT: the whole staged spec text still contains exactly one occurrence of any Kubernetes vocabulary, and it is a disclaimer over untouched text. `grep -nEi 'controller|finaliz|webhook|reconcil|\.status|CRD|etcd|apiserver|RBAC|informer|SandboxClaim|annotation|leader'` over `.spec-changes.md` returns only "finalize block"/"FinalizeWorkspace" (the RPC and the creation block, not a finalizer) plus spec-changes.md:620 "CRD validation rule ... stand as written". — EVIDENCE: proposals/0081_.../0081_...spec-changes.md:620

USEFUL [Standing context / Settled, "Ownership is clean."] and [spec.7.review-kubernetes.1 FACTs]: between them they discharge the §4.6.3 ownership table, the orphan-GC backstop for a gateway replica dying mid-bind, and the "no per-slot sub-state is projected onto a CRD" question (spec/06_warm-pod-model.md:294). I re-confirmed the last one only, and it held; the other two I took on the record, which is the right trade for a lens the amendment does not reach.

OPEN: this lens has now returned nothing on the spec staging four rounds running against text that has changed each time only on the gRPC surface. If a future amendment does not touch a CRD, a status write, a finalizer, a webhook or a reconcile loop, retiring the Kubernetes lens for the remaining spec rounds costs nothing and saves a full round's latency.


### [spec.8.review-mechanism.1]

DECISION: returned an EMPTY findings list — BECAUSE `diff -rq` between the r8 snapshot and the live proposal directory is byte-identical (nothing changed since round 7), and every mechanism-level candidate I derived independently either resolves clean against the tree or is already in the refuted/settled lists — ALTERNATIVES: filing the §5.2 "does not complete" colon-clause (states only the unclean-answer arm, omits the unanswered arm) and the §4.7 row's "precondition on the whole request" versus its own scoping to "the slot release and the runtime teardown" (the recycle scrub is a third operation); both are wording precision in text whose operative disposition is correct, and both sit next to already-refuted siblings.

FACT: the epoch mechanism is coherent and `superseded` IS reachable, by a route no shard states plainly. The adapter's OWN pre-`Runtime.Start` failure branches call `releaseSessionSlot`, which deregisters the entry and removes the tree BEFORE the gateway even learns the RPC failed. So the ordinary ordering is: entry E1 released adapter-side → a client-driven or create-time-reserved retry creates E2 → the lagging compensation names E1 → `superseded`. Per-entry minting is exactly what makes this work, and it is also what makes the lost-`StartSessionResponse` case work (the caller already holds the entry's epoch from `AssignCredentialsResponse`, same entry, same value). A reviewer who reads "per-entry minting was a MISTAKE" in the standing context and stops there will conclude the fence never fires. EVIDENCE: pkg/adapter/session.go:133,:147,:157; pkg/adapter/slotsession.go:214-220.

FACT: the "started-session condition" the edge-case bullets lean on is real and transient, not an idempotent success. `claimSessionSlotUnderLock` returns `codes.Unavailable` "session %s has already started on this pod" for a started entry when `idempotentRepeat` is false, which is the `StartSession` and `Resume` path; only SDK-warm `ConfigureWorkspace` passes `idempotentRepeat: true` and gets `fresh=false` with no error. EVIDENCE: pkg/adapter/slotsession.go:79-86.

FACT: every adapter deregistration site is inside an RPC handler, so §5.2's hold clause "for a hold taken outside any request, by the termination window of the pass that runs the cleanup" describes no path the tree reaches today. It is forward generality rather than an unreachable trigger, and I declined to file it. EVIDENCE: `grep -n "releaseSessionSlot\|deregisterSlot(" pkg/adapter/*.go` returns only resume.go, sdkwarm.go, session.go and slotsession.go itself; podscrub.go deregisters nothing.

WATCHOUT: the two-connection clause in §4.7.1 ("both connections resolve one entry, so both observe one epoch") has a strong reading that is false and a weak reading that is true. On the exclusive path `Binder.Launch` calls `b.reconnect`, dialling a FRESH connection whose only bind-sequence RPC is `StartSession`/`ConfigureWorkspace`; when that RPC fails there is no response, so that connection observed nothing and its compensation is the unconditional form. The weak reading ("the epoch they observe is the same value") is true and the preceding clause ("each connection's compensation names what that connection observed") already covers the failure case, so this is not filable as written — but do not "tighten" it into the strong reading. Also note the exclusive Launch failure runs `failPhase` and retires the pod, so the unfenced form there has no successor to destroy. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:976-1023; spec-changes.md:682.

FACT: `ABORTED` appears nowhere in `spec/` or `docs/` today, so §15.4's status-code choice mints it. There is no gRPC-status classification table in §15.4 or §15.1 for it to contradict; the client-facing tables at spec/15:1023-1137 are REST error codes on a different surface. EVIDENCE: `grep -rn "ABORTED\|Aborted" spec/ docs/reference/adapter-contract.md docs/api/internal.md` returns nothing.

FACT: §4.7.1's "the generation ... is validated on the RPCs that carry it" is consistent with the SPEC even though it diverges from the tree. spec/10_gateway-internals.md:30 says "Pods validate the generation on every gateway→pod RPC", while the standing Settled entry records that the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`. That is a pre-existing spec/code divergence this proposal restates rather than creates. Do not file it against SPEC-5.

FACT: `**Shutdown**` appears in no row of `spec/28_communication-channels.md`, so the "§28's registers" untouched-bullet checks out. EVIDENCE: `grep -n "Shutdown" spec/28_communication-channels.md` returns nothing.

USEFUL [Traps, "The glob `*spec-changes.md` matches BOTH files"]: still true and still the first thing to get right; use full filenames.


### [spec.8.review-operational.1]

DECISION: returned an EMPTY findings list for the operational-consistency lens on the spec staging — BECAUSE `diff -rq scratchpad/cp-snap/.../spec-r8 proposals/0081_...` returns nothing, so the staged spec text this round is byte-identical to the text `spec.7.review-operational.1` read and cleared, and my own independent re-derivation reached the same four refusals it recorded. ALTERNATIVES: I re-opened (a) SPEC-3's `lenny_adapter_leaked_slots` re-citation, (b) the hold's "accounted by the gateway as an ordinary transient slot failure" versus the `ceil(maxConcurrentSessions/2)` drain trigger, (c) the withheld pre-`running` report against §4.7's `ReportSessionScrub` row and §12's `sessions_served` column, (d) the §4.7 / §29.4 co-tenant gating of the §15.4.2 graceful signal against §15.4.2's own table; each is refuted or already adjudicated, evidence below.

FACT: the r7→r8 diff on this proposal is EMPTY. The one r7 finding (the performance lens on the hold's deadline-free directory removal) was refuted rather than fixed, so no fixer text landed. A lens whose r7 shard is clean can verify that in one command instead of re-reading 760 lines. EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../spec-r8 proposals/0081_...` (no output); refuted list carries "The rewritten reclaim hold declares the slot cleanup's directory removal deadline-free".

FACT: the withheld pre-`running` cleanup-outcome report is consistent with every OTHER spec statement of what `ReportSessionScrub` drives, not only with §5.2. spec/04_system-components.md:692 scopes the row to "the per-slot cleanup at a session release" and names the two consumers (`sessionsServed` on `agent_pod_state`, and the `leaked` ledger behind `lenny.dev/drain-request`); spec/12_storage-architecture.md:481 says `sessions_served` is "incremented at each session release (`ReportSessionScrub`)". SPEC-3's append excludes the pre-`running` path from "a session release" in the same breath ("A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome"), so both untouched sites stay true and neither is a missing edit site.

FACT: the adapter emits no counter that could name the reclaim hold, so §5.2's "no report and no counter names it" is true against the tree. `pkg/adapter/metrics.go` registers only the SO_PEERCRED pair, the LLM in-flight gauge, the four credential-rotation series, the control-event counters and the tracing-frame drop counter; there is no bind-refusal or slot-admission series. EVIDENCE: pkg/adapter/metrics.go:15-90.

FACT: no alert rule in `pkg/alerting/rules` keys on slot failure, slot leak, per-slot cleanup or any adapter `Shutdown` outcome, so the staged edits can orphan no alert and need no runbook. The only adapter-metric rule deliberately does NOT key on an agent-pod counter (§16.9, no default scrape target reaches agent pods). EVIDENCE: pkg/alerting/rules/rules.go:1442-1451; pkg/alerting/rules/rules_test.go:506-507.

USEFUL [Standing context #107, #227, #229, review-log.md:2063, :3155]: the pre-existing `lenny_adapter_leaked_slots` inventory hole (named at spec/05:545 and spec/06:160, absent from spec/16, docs/reference/metrics.md and pkg/alerting/rules) and the record that it is gateway-emitted despite its name. Those entries are again the whole reason this lens returns empty rather than filing an inventory finding; a future operational lens should read them BEFORE grepping the metric name.


### [spec.8.review-performance.1]

DECISION: returned an EMPTY findings list for the performance / scalability /
failure-mode lens — BECAUSE the r6→r8 delta is five wording hunks (verified by
`diff -u` against `scratchpad/cp-snap/.../spec-r6`, the newest snapshot whose
`.spec-changes.md` differs; r7, r7-start, r8, r8-start are all byte-identical to
the live file), none of which adds a write, a key, a watch, or a serialization
point, and the capacity half of this lens was already established structurally
inert (Standing context Settled "The amendment adds no control-plane or
data-plane write of any kind"). ALTERNATIVES: I built and dropped four candidates,
each recorded below so nobody rebuilds them.

USEFUL [Standing context Settled #330 "The amendment adds no control-plane or
data-plane write of any kind"]: correct and re-derived. The epoch is an in-memory
`int64` per registry entry, the hold an in-memory map key per slot identifier
(= session identifier), so there is no hot key, no cross-session serialization,
no informer, and no etcd/Postgres/Redis write in the delta. int64 cannot overflow
against `recycle.maxSessionsPerPod`. Do not re-run the write-rate math.

FACT: my own lens's prior OPEN, "Nothing in the staged spec bounds the reclaim hold
on an exclusive pool" (`spec.1.review-performance.1`, Standing context Open), is
DISCHARGED by the r6→r8 rewrite. The hold paragraph now states its own bound
independent of §5.2's `maxConcurrentSessions > 1`-scoped formula: "The hold lasts
as long as the cleanup it covers. The cleanup's close ... is bounded by the graceful
window the reclaiming request carries when the request carries one, by that
request's own deadline when it carries none, and, for a hold taken outside any
request, by the termination window of the pass that runs the cleanup."
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:602. The edge-case bullet at
:196-198 states the same three-way bound, so the two sites agree. A later round can
retire that Open item.

MISTAKE nearly filed, four capacity/reliability dresses this round. Each is
recorded with what kills it so a fifth round does not rebuild them.

(1) "The hold's transient refusal is counted against the pod's §5.2 unhealthy
threshold, so a client retry arriving during the platform's own cleanup retires a
healthy warm pod." The mechanism is real end to end: spec-changes.md:602 stages
"a bind refused this way is accounted by the gateway as an ordinary transient slot
failure"; the create-time-reserved §15.1 retry is one of the three paths the
proposal itself names as able to meet the hold (:193-196); that path reaches
`bindConcurrentSlot`'s reserved branch, which CODE-5 gives an `accountSlotFailure`
caller (non-spec-changes.md:775-777); and `accountSlotFailure`'s body is today's
`health.RecordFailure` + `Unhealthy → DrainSandbox` tail
(pkg/gateway/sessionserver/start.go:2834-2871). At `maxConcurrentSessions` 3 or 4
the threshold is 2, so the pre-existing bind failure plus the hold refusal drain a
pod that a shipped tree would have let the retry re-bind on. What kills it: the
acknowledged reclaim finishes its cleanup and releases the hold BEFORE the gateway
returns the error to the client (Standing context Settled on
`compensateFailedSlotBind` blocking on `cl.Shutdown`), so the client's retry can
only meet the hold in the client-timeout race, and in that race the reclaim is
usually unanswered, which routes to `RecordLeak`/`ExcludePods` instead. It is a
churn-timing change in a narrow window on top of two genuine bind failures, which
is the same shape as the three round-2 capacity dresses already refuted.

(2) "SPEC-5's 'Where both appear on one message, as on `Shutdown`, each is checked
on its own terms' (spec-changes.md:678) makes the §7.1 compensation refusable by
the §10.1 coordination-generation fence, so after a coordinator handoff the reclaim
is unsendable and the slot leaks." Quotes verify, and spec/10_gateway-internals.md:30
does say "Pods validate the generation on every gateway→pod RPC", so a conforming
adapter refuses a stale-generation `Shutdown`. What kills it: that rule is
PRE-EXISTING and already covers `Shutdown`, which carries `coordination_generation`
as field 6 today, so the staged sentence changes no reachability — it restates
§10.1 rather than extending it. Standing context Settled already records both halves
(the shipped adapter validates the generation only in `CoordinatorFence` and
`CheckpointBarrier`; "if `Shutdown` ever starts enforcing the gate, a fenced
attempt's §7.1 compensation becomes unsendable. Nobody has staged anything here").
See OPEN below.

(3) "The staged §5.2 append asserts a leaked slot 'holds its occupancy' with no
durable fallback, so a Redis restart re-admits the pod at full capacity while the
residue stands." Verified against spec/05_runtime-registry-and-pool-model.md:551,
whose rehydration source is `SessionStore.GetActiveSlotsByPod` over Postgres
`state = 'active'` rows, which a failed bind never has. Killed by the proposal's own
adjudication: Standing context Settled records open decision 7 (the Redis
rehydration hole) as resolved to "its own proposal" and moved to
`## Defects in the shipped tree that this proposal does not stage`, on the ground
that `leaked = err != nil || !cleanly` already fires on every ordinary session end,
so the consequential form is shipped and common. Do not re-open it here.

(4) "The hold's 'accounted as an ordinary transient slot failure' sentence is
appended to the concurrency-independent `**Scrub model.**` paragraph while §5.2's
slot-failure accounting lives under a `maxConcurrentSessions > 1` heading." This is
the sixth dress of the family the standing Traps warn about, and it fails the two
tests that family turns on: the sentence names none of the three scoped terms
(`leaked`, the trigger, the gauge), and the case is close to unreachable on an
exclusive pod, because a failed bind there retires the pod under staged §7.1 and an
ordinary session-end `Shutdown` runs its cleanup inside the RPC, releasing the hold
before it answers (spec-changes.md:602, last-but-one sentence).

WATCHOUT: `diff -rq` the WHOLE snapshot directory before reading a delta. The brief
names `spec-r8`, which is byte-identical to the live proposal; the newest snapshot
with a real `.spec-changes.md` delta is `spec-r6`, and the delta is 5 hunks / 86
diff lines. Standing context already says this and it is still true in round 8.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/

OPEN: does the §7.1 compensation need an explicit exemption from the §10.1
coordination-generation fence? spec/10:30 obliges a pod to validate the generation
on every gateway→pod RPC; `ShutdownRequest` carries it; SPEC-5 now affirms that on
`Shutdown` "each is checked on its own terms". So a compensation issued by a replica
whose lease lapsed (the Redis-reset / §12.4 route) is refusable, and the §7.1
obligation cannot then be discharged. The shipped adapter does not enforce it, so
nothing breaks today, and the rule predates this proposal, which is why I did not
file it. A human or a §10.1-scoped proposal should decide whether the compensating
`Shutdown` is exempt.


### [spec.8.review-reliability.1]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE every
reliability angle I could build either resolved against the tree or duplicated an
already-refuted item; the epoch/hold text has now survived enough sweeps that the
remaining candidates are all wording rather than mechanism — ALTERNATIVES: I built and
then killed four candidates, each recorded below so nobody rebuilds them.

FACT: the r6→r8 delta on `.spec-changes.md` is small and entirely reconciliation: the
retry-spends-an-attempt clause in §7.1/Design became "an attempt that meets the hold is one
the retry policy did not place"; the "only rule that refuses a request on the bind path"
claim was deleted from the Design and from the §5.2 hold paragraph; the hold's bound was
re-split (close bounded, directory removal "under no deadline"); §4.7.1's caller rule now
opens with "A `Shutdown` carries an epoch only when it compensates a bind attempt the
caller abandoned"; and the two D1-Option-B pointers became in-place statements of the
shared-entry residue. `spec-r7`, `spec-r7-start` and `spec-r8` are all byte-identical to
the live proposal apart from the review log, so diff against `spec-r6`.
EVIDENCE: scratchpad/cp-snap/0081_.../spec-r6 vs proposals/0081_...

FACT, and it CLOSES the standing Open "Can an adapter restart produce an epoch collision?"
(review-log.md:604). It cannot, and the reason is not the no-re-dial rule. `adapterclient.Dial`
uses `grpc.NewClient`, whose `ClientConn` reconnects transparently, so "the connection the
attempt already holds" would in fact survive an adapter process restart and carry a stale
epoch into a fresh counter that restarts at 1 — the collision is real IF the process can
restart under a live pod. It cannot: the agent podspec sets `RestartPolicy: Never`, so a
crashed adapter container is not restarted and the pod is gone. Record the podspec as the
ground, not the connection rule; the connection rule does not actually carry this case.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:48-50; pkg/controller/sandbox/podspec/podspec.go:980

FACT: the "hold taken outside any request" clause in §5.2's hold paragraph has exactly one
referent in the tree, the §10.1.4 coordinator-hold timeout. `onHoldTimeout` deregisters every
started entry in ONE critical section (pass 1) and then terminates them serially under a single
shared `context.WithTimeout(..., 10*time.Second)` (pass 2), so under the staged rule every one of
those identifiers is held from pass 1 until its own member's cleanup returns. That is what
"the termination window of the pass that runs the cleanup" names. It is unreachable in production
today (the hold cannot arm), so it is not a live bound.
EVIDENCE: pkg/adapter/holdstate.go:189-205,:227-262; pkg/adapter/slotsession.go:375-396

FACT: §5.2's hold sentence "admits no request that would create or resolve a registry entry
under it" is not over-broad against the non-bind RPC surface, and CODE-6's placement of the
refusal in `ensureSlotStateLocked` alone is correct rather than a spec/code gap. The hold starts
at the DEREGISTRATION, so during the hold no entry exists and the "or resolve" half is vacuous:
`Attach`, `Interrupt`, `SendMessage`, `Checkpoint`, `CoordinatorFence`, `ReportUsage` and the
credential RPCs simply find nothing and answer their own not-found. Only the creating half
(the seven bind-sequence RPCs, through `ensureSlotPaths`) is reachable. I nearly filed the
over-breadth as a §15.4 non-conformance of the first-party adapter; it dies here.
EVIDENCE: spec-changes.md:602; pkg/adapter/slot.go:135-148; review-log.md standing Settled #318

MISTAKE nearly filed, and here is what kills it: "an `absent` reclaim is a completed reclaim, so
the §5.2 policy does not exclude the pod, so a policy-placed retry can meet a hold opened by
some OTHER cleanup for the same identifier" — which would falsify the edge-case bullet's
"The retry the §5.2 policy places does not meet the hold". No concurrent cleanup source exists:
every adapter-side release (`releaseSessionSlot` on the pre-start failure branches of
`session.go`, `resume.go`, `sdkwarm.go`, and `DemoteSDK`) runs its cleanup synchronously before
the RPC returns, so the hold is gone by the time the gateway even learns of the failure, and the
only asynchronous cleanup pass is the §10.1.4 hold timeout, which cannot arm.
EVIDENCE: pkg/adapter/slotsession.go:214-220; pkg/adapter/sdkwarm.go:295-301; pkg/adapter/session.go:133,:147,:157

MISTAKE nearly filed: "§7.2 step 3 orders the reclaim before a pod release run by a different
goroutine, so the ordering is unenforceable". Barred by the standing trap on the §7.2 cross-replica
actor problem (four lenses) and inert anyway, because §7.2 steps 1 and 3 have no implementation on
the DELETE path, so correcting step 3 creates no code obligation.

UNVERIFIED: §5.2's hold enumerates two bounds for a request-borne hold ("the graceful window the
reclaiming request carries when the request carries one, by that request's own deadline when it
carries none") and names no bound for a `Shutdown` that carries neither a graceful window nor a
context deadline. `resolveShutdownGrace` then falls back to the runtime-configured grace or the
package default, so the hold is still bounded in the tree but by a figure the spec does not name.
Judged below the bar (no stated budget is breached, and the case needs a caller that sets no
deadline at all). Somebody deciding whether §5.2 should name the fallback should look at
pkg/adapter/mcpruntime.go:311-324.

USEFUL [Settled: the snapshot diffs have been empty for most rounds]: saved a wasted round —
`spec-r8` was byte-identical, and the real delta was two snapshots back.
USEFUL [Settled #319, each bind attempt dials its own connection]: it is what makes the
per-connection epoch latch sound, and it is the first thing to check before building any
epoch-crossing-attempts finding.


### [spec.8.review-security.1]

DECISION: returned an empty findings list for the security lens on the staged spec edits — BECAUSE every security-bearing claim I could falsify checked out against the tree, and the three candidate regressions I built all died on evidence (below) — ALTERNATIVES: I considered filing (a) the withheld `ReportSessionScrub` weakening the `maxSessionsPerPod` residual-state bound, (b) the reclaim hold over-reaching to credential RPCs, and (c) §7.2 step 3 releasing an unreclaimed pod to the pool; each is refuted or is adjudicated ground.

FACT: `diff -rq scratchpad/cp-snap/.../spec-r8 proposals/0081_...` returns EXIT 0 — the round-8 snapshot is byte-identical to the current proposal, so there was no fix stage to read hardest. Do not spend a tool call re-diffing; read the whole document instead. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r8

FACT: the withheld pre-`running` cleanup-outcome report costs NO leak signal, because the shipped adapter derives the scrub outcome from the runtime close alone and there is no runtime close on the pre-`running` path. `reportSessionScrub(ctx, sessionID, closeErr)` and `sessionScrubOutcome(closeErr)` map `closeErr == nil` to `RELEASED`, and the tree-removal error is discarded at `_ = removeSlotTree(st)`. So a pre-`running` cleanup would have reported `RELEASED` regardless, and withholding it withholds only `IncrementSessionsServed`. A future reviewer who builds "the staged rule hides a failed credential-directory removal" will find the hiding is pre-existing and orthogonal. — EVIDENCE: pkg/adapter/sessionscrubreporter.go:38-43,:61-84; pkg/adapter/session.go:271,:279

FACT: SPEC-3's widened slot-cleanup action list is accurate against the tree. `slotlayout.RemoveTree` removes `p.slotRoot()`, `p.Sessions`, `p.Artifacts` and `p.CredentialsDir` best-effort, so the staged "removes the slot's credential directory `/run/lenny/slots/{sessionId}/`" is shipped behaviour rather than a new obligation. — EVIDENCE: pkg/adapter/slotlayout/tree.go:59-68

FACT: there is a FOURTH `removeSlotTree` caller nobody's shard names — the §10.1.4 coordinator-loss hold's self-termination pass, which deregisters and cleans up outside any request. The staged §5.2 hold text already covers it ("for a hold taken outside any request, by the termination window of the pass that runs the cleanup"), so it is not a gap, but a reviewer enumerating cleanup paths from `Shutdown` and `releaseSessionSlot` alone will miss it. — EVIDENCE: pkg/adapter/holdstate.go:229-254; pkg/adapter/slotsession.go:214-220; pkg/adapter/session.go:271

FACT: `DemoteSDK`'s entry removal DOES run the per-slot cleanup inside the RPC, via `releaseSessionSlot` → `removeSlotTree`, so §5.2's "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it" is grounded. This also closes the security question "can an entry be released without a cleanup, so a successor inherits residue at a fresh epoch?" — on every path checked the answer is no, and that is what makes the `superseded` outcome safe. — EVIDENCE: pkg/adapter/sdkwarm.go DemoteSDK body (`s.releaseSessionSlot(sessionID)`); pkg/adapter/slotsession.go:214-220

FACT: the reclaim hold is per-slot-identifier, so it has no cross-tenant or co-tenant availability effect on a concurrent pod, and the epoch is compared only against the entry for the session the request names. Neither mechanism widens a trust boundary: the adapter enforces the fence on itself, and the outcome enum it self-reports replaces `exited_cleanly`, which was equally adapter-reported, so the leak ledger's authority is unchanged rather than newly delegated to the pod. — EVIDENCE: pkg/adapter/session.go:238-241; spec/06_warm-pod-model.md:160

WATCHOUT: the staged §4.7 `Shutdown` no-op sentence ("A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response") looks like it suppresses the occupancy-zero recycle scrub, because that request always names a session the adapter holds no entry for, and the whole-pod scrub carries the mandatory step-0 credential purge. It does not: the sentence enumerates the two teardowns, and the row's UNEDITED remainder states the recycle disposition's scrub unconditionally on the same row. Check the unedited tail before building this one. — EVIDENCE: spec/04_system-components.md:686 ("On the **recycle disposition** ... runs the §5.2 whole-pod scrub asynchronously"); spec/05_runtime-registry-and-pool-model.md:461

WATCHOUT: "released back to the pool" in the staged §7.2 step 3 reads as returning a possibly-unreclaimed pod to shared inventory, which would be a cross-tenant residue finding. It is the spec's established LOOSE wording for the terminal claim disposition the §6.2 fence drains; spec/06:283 uses the same phrase and the standing context already records the fence as governing. — EVIDENCE: spec/06_warm-pod-model.md:283; review-log Standing context, "§6.2's pod fence retires a failed pod"

OPEN: `spec/10_gateway-internals.md:30` says pods validate `coordination_generation` "on every gateway→pod RPC", while the review-log Standing context records that the shipped adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`. The staged §4.7.1 sentence ("is validated on the RPCs that carry it") sides with the spec, so it is not a defect of this proposal, but the spec/code divergence is real and unowned. Somebody should file it separately rather than inside 0081.

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
