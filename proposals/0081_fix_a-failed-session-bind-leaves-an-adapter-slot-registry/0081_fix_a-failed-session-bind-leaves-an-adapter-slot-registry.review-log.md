# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 11).** Pass 11 read the same window pass 10 read, which was never
archived, plus the entries filed after pass 10 ran: the thirteen `spec-recheck.1` lens shards, the
`f1.open-decisions.human-decisions.15` firing, the second `f1.open-decisions.out-of-scope-defects.threshold-retune`
firing, `f1.summary-cleanup`, `non-spec.1.fix-schema-1` and the third index-and-checklist
reconciliation pass. Lifted: fourteen new Settled entries, nine new Traps and four new Open items.
Applied four CORRECTS against the standing context. (1) Open decision 15 (the mid-resume
carve-out) was resolved as "no carve-out" and deleted, so `## Open decisions for human to make`
now carries 11, 16, 17 and 18; every entry saying "11, 15, 16, 17 and 18" is rewritten and the
preamble's carried-out set is 16 through 18. (2) The Open "Does §5.2's hold obligation reach the
sixteen non-`Shutdown` release sites?" rested on "CODE-6 takes the hold only inside `Shutdown`",
which the staged text contradicts: every deregister-then-destroy site goes through
`reclaimSlotLocked`, and there are three. Retired. (3) The Open "Can the gateway placing a §5.2
retry actually see that a pod holds an incomplete reclaim?" is answered YES for the scope the
staged sentence carries; the residual is the separate placement-guarantee Open. Retired.
(4) The `f1.summary-cleanup` firing reports `## Impacts on other proposals` carrying four rows for
0080 against the section's own one-row-per-proposal rule, which disagrees with the older
"nine rows with no duplicate subject" entry; the newer reading is kept and the older is noted.
Retired as closed: three Open items and no Deferred (the three reconciliation passes closed their
Deferred entries before pass 10, and the four unapplied prose repairs the third pass lists are
already carried here). Did NOT reach 691 lines: this section is 954. It grew because this
window opened the non-spec lane's first security, reliability and test-coverage passes since the
hand amendment, and because one FILED finding new to this pass (CODE-4's session-keyed credential
release destroying a concurrent successor attempt's leases) needs its two look-alike refutations
recorded beside it or the next round refutes a live defect with an unrelated refutation.

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
- **The whole summary structure was rebuilt and verified by the `f5` cleanup firing.** The required sections stand in the required order, `## Open decisions for human to make` carried entries 9 and 11 at that firing (it now carries 11, 16, 17 and 18 — see the correction below), `## Defects in the shipped tree that this proposal does not stage` carried eight entries then and carries thirteen now, `## Impacts on other proposals` carries nine rows (four for 0080, one each for 0073, 0075, 0078, R1b and R12), and `## Deliverable index` is last. CORRECTED: "no duplicate subject" is the `f5` reading and the later `f1.summary-cleanup` firing reads the same four 0080 rows as a breach of the section's own one-row-per-proposal rule, left standing because merging them is a rewrite rather than a move. A pass that consolidates them must carry all four assessments (§1.2, §1.19, §1.7, and the grouped §1.1/§1.3/§1.4/§1.5/§1.16/§1.20 row) into the merged row. Nothing was relocated because nothing was misplaced.
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
- **DECISION: the shared-entry residue is ACCEPTED and unstaged, and NO lettered or numbered decision is minted for it.** The two "open decision D1's Option B" citations were false (no D1 and no Option A/B exists anywhere) and are replaced by a descriptive statement of what closing would cost: a per-ATTEMPT discriminator carried on every request that can create or resolve a slot entry, which this proposal does not stage. CORRECTED THREE TIMES: the section now carries entries 11, 16, 17 and 18, each keeping its original number and none renumbered; entries 9, 12, 13, 14 and 15 were deleted as resolved. Entry 17 is the re-put of entry 9 against the converged (worse) text and is the only entry carrying a recommendation. Any statement that the section carries "9 and 11", or "11 and 12", or "11, 15, 16, 17 and 18", or that entry 11 is the sole open decision, is reading a superseded pass. The preamble's own carried-out-of-the-log set is entries 16 through 18, rewritten when 15 left.
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
- **DECISION (`f1.open-decisions.15`): §7.1's exclusive-pod clause needs NO mid-resume carve-out, and entry 15 is deleted.** The staging is already correct: §7.1 closes with "The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated", and SPEC-2's §7.2 step 3 states no pod outcome by design. No staged text changed.
- **§6.2's `resuming` failure-transitions subsection is self-declared authoritative, at spec/06:229, and :234 states the outcome.** ":229" declares it "the **authoritative enumeration** of every edge out of the session-model `resuming` state" and ":234" says the half-claimed replacement pod is released to the pool. An earlier design cited :230 for the first; it is :229.
- **A released half-claimed pod's residue is disposed of in BOTH pool configurations by shipped text.** Non-recycling: spec/06:80 projects a claim deleted on a `recycle.enabled: false` pod through `draining` to `terminated`. Recycling: spec/05:453 runs the whole-pod scrub whenever occupancy reaches zero before reuse, and `podsession/binder.go:498-501` (the `Recycle` field doc comment) records that trigger on the release path.
- **The occupancy-zero recycle boundary already waits for slot cleanup.** spec/06:175 reads "When occupancy reaches zero **and all slot cleanup has finished**, the pod leaves `claimed`", which kills the "the hold deregisters the entry, occupancy hits zero, and the whole-pod scrub races the per-slot cleanup" dress before it is built.
- **§15.4 now PUBLISHES the hold refusal's status code, and the older refutation is stale.** The staged block reads "refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on". `grep -rn ABORTED spec/ docs/ schemas/` is empty, so this is the corpus's first spec-level gRPC status on the adapter surface; the only §15.4 precedent is the `UNIMPLEMENTED` mention in the SDK-warm demotion paragraph (spec/15:1469). `codes.Aborted` is already live on the same service for the busy checkpoint op lock (checkpoint.go:115, oplock.go:37), on a disjoint RPC set, so there is no collision.
- **§7.2 orders the step-3 reclaim BEFORE step 4's `coordination_generation` bump** (spec/07:213 then :214), so the compensating `Shutdown` carries the generation the pod still holds and the bump cannot fence it.
- **A §7.4 mid-session upload refused by the hold surfaces to the client as HTTP 502 `UPSTREAM_ERROR`,** on both the `PrepareWorkspace` and the `FinalizeWorkspace` arm, so the staged "the transient classification a caller retries on" holds for that caller too. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:70,:126-137.
- **`superseded` is already bound elsewhere and does not collide.** It is a `manifest_reason` enum value in the §10.1 partial-checkpoint model (spec/10:148,:157; spec/16:198; spec/29:969), a different enum on a different message.
- **"Registry entry" and "the adapter's slot registry" are already spec vocabulary** (spec/16:188, spec/28:861), so SPEC-5's heavy use of the term mints nothing undefined.
- **§28.7's wire-contract artifact register is one row per ARTIFACT under `schemas/`,** derived from the directory rather than per message or field (spec/28:1759-1765,:1781-1783), so SCHEMA-1's nine additive fields and one enum add no §28.7 row.
- **Four tier-0 proto gates were checked against SCHEMA-1 and none fires.** `claim_register_proto_agreement_test.go` inspects only rows whose `surface` contains `schemas/lenny-adapter.proto` and both staged rows name Go files; `adapter_proto_message_scope_test.go` keys on the `SessionId`/`session_id` pairing; `adapter_proto_generation_scope_test.go` counts `operationalFenceSentence` occurrences at twelve and no staged comment carries it; `adapter_session_address/session_address_wire_test.go` checks `session_id` presence and `reserved 4`. The one gate that DOES fire is the tier-3 `TestShutdownMessagePostRemovalDescriptor_spec_4_1`.
- **S10 declares `Tiers 0, 1, 3, 7a, 9`,** which is what closed the tier-3 step-ordering finding (the tier-3 suite is titled for SCHEMA-1, CODE-1 and CODE-6, and only S8 and S9 named tier 3 before). Any later edit that moves the two `Shutdown` outcome cases must move that tier 3 with them.
- **`recycle.maxSessionsPerPod` retirement is advanced by `sessionsServed`, which `RecordSessionScrub` increments,** so SPEC-3's withheld report genuinely does not advance pod retirement for a bind abandoned after `receiving_uploads`, even though that bind ran `RunSetup` and wrote a credential file. Judged "less strict rather than a control regression": the design states it and §5.2 scrub step 0's credential purge is the backstop. A reopening must argue from residue step 0 does not reach.
- **The threshold-retune scope call has a third ground, and its citations moved.** The shipped code CONFORMS to spec here (spec/06:160's rolling-window failures plus persistent leaks reaching `ceil(maxConcurrentSessions/2)` is what the clamped `(maxConcurrent+1)/2` computes), so a defects row would have no divergence to cite. Current citations: the Non-goals bullet at summary.md:315-316, the "Faster pod churn" bullet at non-spec-changes.md:1954-1963, `UnhealthyThreshold` at slothealth.go:215, `Tracker.Unhealthy` at :136, the sole production trigger at start.go:2858. The `:1764-1773`, `:214-220` and `:2857` citations in the earlier firings have drifted.

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
- **FILED and live: CODE-4's credential release is keyed on the SESSION, so it can destroy a concurrent successor attempt's leases, and two standing entries look like they refute it and do not.** The `materializeSlot` wrapper runs `compensateFailedSlotBind` first, blocking up to `slotCleanupBudget` on a `context.WithoutCancel`, and only then calls `b.releaseCredentials(req.SessionID)`, which iterates `LeasesBySession([sessionID])` and releases every lease under that key (credassign.go:400-410), while `assignSlotCredentials` mints per attempt under the same key (slotbinder.go:379). The design's own headline scenario, a client whose context expired retrying into the same slot, puts attempt 2 past `assignSlotCredentials` inside that window: the epoch fences the pod-side reclaim to `superseded` and the very next statement destroys the successor's leases anyway. Look-alike (1): the Trap "Do NOT make the bind-path lease release conditional on `assignSlotCredentials` having SUCCEEDED" bars a STAGE condition; this is the release's KEY, and releasing exactly the lease ids this attempt minted stays unconditional over a partial assign. Look-alike (2): the refuted "CODE-4's unconditional `releaseCredentials` releases leases a snapshotless resume-rebuild may still hold" died on "the rebuild re-mints on every attempt"; a concurrent second `/start` does not re-mint after the release. The proposal states the correct rule for the resume path and does not carry it to the bind path (non-spec-changes.md:701-704).
- **Do NOT cite "§15.4 names no status code for the hold refusal" as evidence of a gap.** Three earlier findings were refuted on that premise and a later fix CLOSED it: §15.4 now publishes `ABORTED` and the two non-conformance cases. Reading the refuted list as a map of what the text still lacks misleads in this direction; re-read the text. The same staleness runs the other way for anyone quoting the refutations of "the missing wire spelling of an epochless `Shutdown`".
- **The snapshot trap has a SIXTH form: `diff -rq` can report the spec staging as unchanged when it changed.** In one round `diff -ru <snapshot> <proposal dir>` listed only the checklist and the non-spec file, while a direct `diff -u` of the two `.spec-changes.md` files showed a real delta. Diff the single file explicitly rather than trusting the recursive listing, and remember that mtime order across `scratchpad/cp-snap/0081.../` is not round order (`spec-r4` sorts newer than `spec-r9`) and that this window reuses the previous window's directory names.
- **Do NOT move the §7.2 reclaim after step 4.** It sits at step 3, before the `coordination_generation` bump, and that ordering is the only reason a mid-resume compensation is not fenced by the bump it precedes. A later edit that reorders them makes every mid-resume reclaim fail the generation check and leak the slot.
- **The `## Impacts on other proposals` section breaches its own one-row-per-proposal rule with four 0080 rows, and that was left deliberately.** Merging them is a rewrite rather than a move. A pass that consolidates must carry all four assessments into the merged row; a pass that "fixes" it by deleting three loses three assessments.
- **The SCHEMA-1 window under `**Decisions.**` and the R1b row under `## Impacts on other proposals` both assert the S-2 second-window precondition.** They agree today and are deliberately not folded into each other: the decisions bullet carries it as this proposal's step-ordering constraint, the impacts row as an assertion about the programme step. An edit to one owes the same edit to the other.
- **MISTAKE nearly filed, three shapes against the reclaim hold's reach into non-bind RPCs.** `RevokeCredentials` answering `codes.FailedPrecondition` mid-hold where §15.4 calls a permanent status non-conformant; `extendCredentialLeaseSlot` returning an empty success; `Attach`/`Interrupt`/`ReportUsage` answering the same permanent code. All three die on the same two grounds already recorded for the wide predicate, and the natural remedy is a §15.4 wording tightening, which is spec-lane. Anyone reopening must attack the "create or resolve is this change's own bind-sequence vocabulary" scoping directly. EVIDENCE: pkg/adapter/slotcreds.go:102-146; slotsession.go:274-283.
- **Do NOT read the refuted-list entries about the shared-entry epoch inheritance as current.** Six of seven shapes one lens re-derived from the amendment text were refuted as STALE against a pre-`2aca9851b` snapshot rather than on their merits. Diff against the newest differing snapshot and grep the load-bearing quote before deriving anything from remembered text.
- **The standing context is where the answers are, and reading it end to end is the cheapest first move.** Six of seven candidates a fresh reader generates on this document are already in it, several as explicit "do not re-derive" entries, and four consecutive lens shards this window recorded that reading it first turned a full re-derivation into a spot check. The refuted-family index in Traps is the highest-value part; keep it at length and do not split the consolidated security-family and capacity-family entries back into individual lines.

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
- **Does §15.4 state what the unconditional (no-epoch) `Shutdown` reports on its response?** — OPEN: §4.7's row does (`reclaimed` / `absent`) and the gateway reads one field on every answer, but §15.4 scopes its whole outcome paragraph to "the comparison's outcome" after saying the no-epoch form "is compared against nothing". A third-party adapter author working from §15.4 alone has to derive it. See `spec.1.review-citations.1` and `spec.1.review-client-surface.1`.
- **The `/finalize` Gap-2 window cannot discharge §7.1's reclaim obligation** — OPEN, FILED: both branches fail after `Binder.Prepare` closed its connection and call `ReclaimClaimed`, which sends the adapter no RPC, while §7.1 names the creation finalize block, mandates the reclaim on the connection the failed stage still holds, and forbids re-dialling. The fixer must pick one of two legal answers and make §7.1, §4.7.1's caller rule and the accepted-failure-mode list agree: relax the no-re-dial rule for the epochless form, or record the window as an accepted residue. Keep the finding on the CONNECTION, never on the pod disposition. See `spec.7.review-mechanism.8`.
- **§4.7.1's caller rule mixes per-connection and per-session granularity** — OPEN, FILED: the holding rule defines one per-connection value ("the most recent epoch a response reported to it on one adapter connection") while the naming rule two sentences later reads it per session ("the epoch the caller holds for that session"). Newest, least-examined block. See `spec.8.review-fresh.1`.
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
- **CODE-4's session-keyed credential release versus a concurrent successor attempt** — OPEN, FILED: the wrapper's `b.releaseCredentials(req.SessionID)` releases every lease under the session key after a compensation that can block for the whole budget, and a second `/start` attempt for the same session is past `assignSlotCredentials` inside that window. The remedy is to release the lease ids this attempt minted rather than the session's. See `non-spec.1.review-reliability.1` and the Trap that records its two look-alike refutations.
- **What §15.1 error a client sees when its own `/start` retry on a create-time-reserved slot meets the hold** — UNVERIFIED: the proposal lists §15.1's error catalog as deliberately untouched on the ground that the refusal mints no client-visible code, and nothing in either lane names the catalog row the gateway answers with or shows it is transient on that path. See `spec-recheck.1.review-client-surface.1`.
- **Does `superseded` fix a value for `exited_cleanly`?** — UNVERIFIED: §5.2 states the leak detector as "the `Shutdown` response for that reclaim does not report a clean exit" while §7.1 scopes the same predicate to "answered `reclaimed` without reporting a clean exit". Nobody has stated on the record what `exited_cleanly` carries on a `superseded` or an `absent` answer. See `spec-recheck.1.review-docs-alignment.1`.
- **Does the widened leak criterion mass-retire pods on a transport blip?** — UNVERIFIED: an incomplete adapter reclaim now produces a PERSISTENT leak where the shipped path produced a windowed failure that decayed in five minutes, so a fleet-wide gateway-to-adapter blip retires one pod per event at `maxConcurrentSessions: 2`. Judged not a finding (the residue is genuinely unreclaimable and §6.2 prescribes the persistent disposition), and nobody has measured the replacement rate it implies. See `spec-recheck.1.review-performance.1`.

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

Retired in compaction pass 11, all closed rather than dropped:

- The OPEN "Does §7.1's exclusive-pod clause need a mid-resume carve-out?". Answered NO by the `f1.open-decisions.human-decisions.15` firing and deleted from the summary as open decision 15. §7.1 already ends with the pre-attached disposition governing the pod and this reclaim governing the slot state, and §7.2 step 3 states no pod outcome by design, so no staged text changed. The pod's disposition is enumerated by §6.2's self-declared authoritative `resuming` subsection and disposed of in both pool configurations by shipped text; both facts are in Settled.
- The UNVERIFIED "Does §5.2's hold obligation reach the sixteen non-`Shutdown` release sites?". Retired as STALE in the proposal's favour: it rested on "CODE-6 takes the hold only inside `Shutdown`", and CODE-6's current text routes every site that deregisters an entry it then destroys through `reclaimSlotLocked`, of which there are three (`Shutdown`, `releaseSessionSlot`, and the §10.1.4 hold termination). The staged code implements the universal obligation rather than a `Shutdown`-only subset, so the spec-versus-code contradiction the item anticipated does not exist.
- The UNVERIFIED "Can the gateway placing a §5.2 retry actually see that a pod holds an incomplete reclaim?". Answered YES for the scope the staged sentence carries: §5.2 governs "the retries this policy places", and `applySlotRetryPolicy` places them inside one client request holding the `*SlotBindRequest` whose `ExcludePods` the same iteration appended after reading `sbe.Leaked`. The residual, that a SECOND client request cannot see the first one's incomplete reclaim, is the separate standing Open on the placement guarantee.

One item retired as MOOT rather than closed, kept so nobody re-derives it: the standing Open on `examples/runtimes/echo/` stays retired, and this window added the reason spec/15 is self-inconsistent about it as well as wrong about the tree — :1466 calls it a reference implementation of the ADAPTER while :1491 and :1812 describe it as a RUNTIME writing JSONL to stdout. Pre-existing spec fiction, not 0081's.

## Ledger

### [spec-recheck.2.fix-G1.1]

DECISION: closed the `running`-boundary contradiction by deleting the start-handover sense of "acknowledge" rather than by editing the §6.2 fence annotation — BECAUSE the tree's boundary is `Runtime.Start` returning followed by `noteRuntimeStarted` (pkg/adapter/resume.go:140-144; pkg/adapter/runtimegeneration.go:36-49), which is what both "session dispatched to runtime with its session identifier" (spec/06_warm-pod-model.md:152-153) and "the pod's shared runtime process has been given the session" denote; the only text naming a third, later moment was the clause "whose session the runtime has not yet acknowledged", and no runtime-side acknowledgement of a start exists in pkg/adapter — ALTERNATIVES: (a) reword the `receiving_uploads ──→ running` fence annotation, rejected because it forces DOCS-1 to edit docs/reference/state-machines.md:235 as well as add its row and strands pkg/sandbox/slotstate's `Running` doc comment; (b) delete the "which is the moment the trigger above names" attribution and state the boundary on SPEC-4's own authority, rejected because it removes the attribution rather than the disagreement and leaves the "acknowledged" gloss in place.
FACT: four sites in the staged spec changes now read off one boundary, all in the proposal only, no spec/ or docs/ file gains an edit: the Design `Shutdown` paragraph ("rather than from the moment the session reaches the runtime"), the Design state-machine paragraph, SPEC-1's staged §4.7 `Shutdown` row, and SPEC-4's staged **Pre-`running` slot cleanup.** paragraph. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:20,89-93,324,661
WATCHOUT: do not sweep "acknowledg*" across this proposal. Every surviving use in the spec-changes file (lines 159, 194, 211, 463) and every use in the summary and non-spec-changes files means "the adapter did not answer the compensating `Shutdown`", which is correct and load-bearing. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:159,463
FACT: the §4.7 runtime-teardown precondition (the adapter's ADMISSION of the start RPC) and the §6.2 `running` boundary (the session reaching the runtime) are deliberately two different moments, and the staged text says so. A later fix must not collapse them. EVIDENCE: same file:18-25
USEFUL [standing context, "DECISION: the `running` boundary is the runtime having been given the session"]: its UNVERIFIED item asked a later reviewer to confirm the four sites agree. They now agree, in the trigger's own vocabulary, and the fence annotation is still untouched, so the neighbouring entry "the staged new-edge annotation is left mirroring that vocabulary" also still holds.


### [spec-recheck.2.fix-design-G1.1]

DECISION: close the "`running` boundary contradicts the unedited `receiving_uploads → running` trigger" finding by deleting the word "acknowledge(ment)" from every site that uses it for the START handover, and restating the boundary in the fence trigger's own vocabulary ("the session has been dispatched to the pod's shared runtime process with its session identifier"), keeping the attribution clause "which is the moment the trigger above names" — BECAUSE the trigger, the staged prose and the tree all name ONE moment (the handover completing) and the only text naming a third, later moment is the word "acknowledged"; keeping the attribution is what holds DOCS-1 to the single added row and leaves spec/06:152 and docs/reference/state-machines.md:235 untouched — ALTERNATIVES: (a) editing the fence annotation to say "has been given", which the review log bars at review-log.md:488 and which cascades into docs/reference/state-machines.md:235, DOCS-1's edit list and CODE-3's doc comment; (b) deleting the attribution clause and asserting the boundary on SPEC-4's own authority (the finding's fallback), which leaves §6.2 with two unrelated statements of the same boundary and invites the next round to ask which governs.

FACT: the tree's boundary is `Runtime.Start` RETURNING, not an acknowledgement signal. `pkg/adapter/resume.go:140-144` calls `s.Runtime.Start(ctx, sessionID)` and only then `s.noteRuntimeStarted(sessionID)`, which is the sole writer of `runtimeLive` (`pkg/adapter/runtimegeneration.go:36-44`, field doc at `pkg/adapter/server.go:348-351`: "the set of sessions the adapter has started on the pod's one shared runtime process and not yet finished closing"). A start still in flight is a `Runtime.Start` that has not returned. There is no runtime-side acknowledgement of a start anywhere in `pkg/adapter`; the "runtime acknowledgement" hits in spec/04, spec/07 and spec/15 are the unrelated interrupt / CH-RUNTIMEOPS mechanism. — EVIDENCE: pkg/adapter/runtimegeneration.go:36, pkg/adapter/server.go:348

WATCHOUT: "acknowledged" carries TWO senses in this proposal and only one is being removed. "an unacknowledged reclaim" (summary.md:138, non-spec-changes.md:800 and ~15 more sites) means the ADAPTER did not answer the compensating `Shutdown`, and is correct and untouched. Only the three START-handover uses change: spec-changes.md:21, :93 and :661. — EVIDENCE: proposals/0081_.../*.spec-changes.md:324 versus :661

FACT: the four sites the standing context asks a reviewer to confirm agree do agree once "acknowledged" goes, because the other two already use the handover vocabulary: CODE-3's doc comment reads "bind abandoned before the runtime is given the session" (non-spec-changes.md:485) and DOCS-1's new row reads "before the runtime has been given the session, a start still in flight included" (non-spec-changes.md:1249). Neither needs an edit. — EVIDENCE: proposals/0081_.../*.non-spec-changes.md:1249

CORRECTS [review-log.md:533]: that entry states a round wanting this closed must EITHER edit the fence annotation OR delete the attribution clause. There is a third route it did not consider and it is the cheapest: leave both, and delete the "acknowledged" gloss that is the only text naming a moment later than the trigger's. The entry's own "reconciling reading" (a start in flight is the window before anything is handed over) is exactly what the reworded prose then says out loud.

DEFERRED [docs/reference/state-machines.md]: none. Row :235 ("the session is dispatched to the runtime with its session identifier") stays true under this fix and is deliberately not edited; it is the mirror of spec/06:152, which is also not edited.


### [spec-recheck.2.review-applicability.1]

FACT: The spec-changes file did NOT change between spec-recheck r1 and r2. `diff -rq scratchpad/cp-snap/.../spec-recheck-r2 proposals/0081_.../` reports exactly one differing file, the review log. So round 2 re-read text that round 1 already cleared; the delta this loop advertises was empty for the spec lane. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r2/0081_...spec-changes.md (byte-identical to the working copy)

FACT: Every "text to replace" anchor in the spec staging still matches the tree byte for byte and occurs EXACTLY ONCE in its target file, verified mechanically this round with a python `str.count` over the fourteen anchor strings. The single non-unique one is §29.4's `([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).`, which occurs three times (spec/29:645, :711, :982); the instruction's "In §29.4's numbered step 13" scoping is what resolves it, and step 13 ends at :711. EVIDENCE: spec/29_communication-scenarios.md:645,711,982
FACT: The four insertion points the amendment added all resolve uniquely and have nothing between the named neighbours: §4.7.1's `*Adapter → Gateway RPCs:*` table closes at spec/04_system-components.md:693 with `#### 4.7.2` at :695; §15.4's `**SDK-warm demotion contract:**` is spec/15_external-api-surface.md:1469 with `#### 15.4.1` at :1471. EVIDENCE: spec/04_system-components.md:688,695; spec/15_external-api-surface.md:1469,1471
FACT: All markdown anchors the staged text mints resolve to real headings, including the two SPEC-5 introduces into new text: `#471-role-and-gateway-rpc-contract` (spec/04:659) and `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848). Both are already used by spec/README.md, so they are established rather than newly minted. EVIDENCE: spec/README.md (one use each)

FACT: The §4.1 commentary's proto citation is exact. `schemas/lenny-adapter.proto:1630-1635` is precisely the `coordination_generation` doc comment plus `int64 coordination_generation = 6;`. EVIDENCE: schemas/lenny-adapter.proto:1635

FACT: The one tier-11 gate that reads the §6.2 per-slot fence, `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`, asserts only PRESENCE of four named general edges and ABSENCE of those four from the concurrent-occupancy block. Adding `receiving_uploads ──→ slot_cleanup` to the general block turns it nothing. Its `requireLine` probes are `"Per-slot sub-states (tracked per session"` on §6.2 and `"per-slot sub-states"` on §7.2; no staged sentence introduces a second matching line in either section, so the SPEC-2 §7.2 edits do not make those probes ambiguous. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-36,:50-80

WATCHOUT: A tempting but weak filing is "§5.2's hold refuses every request that would create or resolve an entry, while §15.4 exempts `Shutdown`, so the two sections disagree". It does not hold: the hold begins AT the deregistration, so during the hold there is no entry, and a `Shutdown` neither creates nor resolves one. §15.4's `Shutdown` carve-out is a clarification of a rule that never reached it. Do not re-file this without a harm the no-entry reading does not kill. EVIDENCE: 0081_...spec-changes.md:603 ("from the critical section that deregisters"), :713 ("`Shutdown` is not held")

OPEN: S3 (SPEC-2) lands a §7.1 paragraph asserting "[Section 5.2] states that the adapter holds that identifier from the deregistration…" while §5.2's reclaim-hold paragraph lands at S4, and S3's Depends-on names only S1. S1's line explicitly discloses its own equivalent forward citation; S3's does not. The linked anchor still resolves so no gate fires, and the fix is a checklist reorder or a disclosure line — both outside this loop's scope. Whoever owns the checklist reconciliation should either swap S3/S4 or give S3 the same disclosure sentence S1 carries. EVIDENCE: 0081_...implementation-checklist.md:6,10-11; 0081_...spec-changes.md:392

DECISION: returned an empty findings list — BECAUSE the staging is byte-identical to text this lens already cleared, and this round's independent re-derivation (anchor uniqueness, insertion-point determinism, anchor resolution, created-artifact property sweep, gate state over the two spec-reading tier-11 tests) found nothing new — ALTERNATIVES: filing the S3 forward citation (out of scope: fix lands in the checklist) and the §5.2/§15.4 `Shutdown` hold tension (refuted above on its own terms).


### [spec-recheck.2.review-citations.1]

FACT: spec-changes.md did NOT change between the spec-recheck-r2 snapshot and this round; `diff -rq` shows only the review log differs. The "delta first" reading order had no delta in this lane. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r2 vs proposals/0081_.../
FACT: every file:line citation in the spec staging resolves exactly. Verified: schemas/lenny-adapter.proto:1630-1635 (`coordination_generation = 6` on `ShutdownRequest`, comment at 1630-1634, field at 1635); spec/04_system-components.md:151 (field-set derivation) and :157 (the `ShutdownRequest` paragraph whose third sentence SPEC-1 replaces, quoted verbatim); spec/29_communication-scenarios.md:586-588 (`**Preconditions.**` "so the runtime is running"), :589-591 (interrupt path addition), :669-674 (step 10 "valid in any non-terminal state" cited to §15.1), :697 ("the adapter closes the session runtime").
FACT: every verbatim replacement anchor in the staging matches the tree byte-for-byte. spec/04:686 (`Shutdown` row opening), spec/04:854 (§4.7.9 step 5), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**`), spec/06:234 (mid-resume cancel clause), spec/07:23 (atomicity parenthetical), spec/07:210 (§7.2 preamble premise sentence), spec/07:213 (step 2 tail), spec/07:214 (step 3), spec/07:414 (§7.3 list item 4). Insertion points also check out: spec/04:688-695 (§4.7.1 tables → §4.7.2), spec/06:156-158 (fence close → `**`reserved` hold semantics.**`), spec/15:1469-1471 (SDK-warm demotion → §15.4.1).
FACT: the code-attributed claims in the spec staging are all true in the tree. `slotlayout.RemoveTree` sweeps `CredentialsDir` (pkg/adapter/slotlayout/tree.go:58-68); `deregisterSlotLocked` cancels every armed timer then deletes the entry (pkg/adapter/slotsession.go:174-188); `stageWorkspace` sends `PrepareWorkspace` only when `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1322-1328); `DemoteSDK` removes the entry via `releaseSessionSlot` (pkg/adapter/sdkwarm.go:296-301); `concurrent_slots_exhausted` is shipped (pkg/gateway/sessionserver/start.go:185); the retry path marks the slot leaked when `ReleaseSlotReservation` fails and `BindReservedSlot` only logs (pkg/gateway/sessionserver/start.go:2834-2848 vs pkg/gateway/podlifecycle/podsession/slotbinder.go:217-220); §11.4's full-revoke fan-out does send §4.7 `Shutdown` (spec/11_policy-and-controls.md:270); `Binder.ReleaseSlot` sends the unfenced form (pkg/gateway/podlifecycle/podsession/slotbinder.go:542).
FACT: the decomposed bind really does span two adapter connections — `Bind`'s doc comment says Claim/Prepare/Launch "each reconnects to the claimed pod from the persisted binding (§4.6) rather than holding one connection across the whole window" (pkg/gateway/podlifecycle/podsession/binder.go:764-769). This is what makes §4.7.1's two-connection paragraph accurate rather than hypothetical.
FACT: the adapter's `Shutdown` handler cites §15.4.2 for the pod-global graceful-shutdown signal and gates it on `!boundRemains` (pkg/adapter/session.go:250-261), so the proposal's "§15.4.2 graceful-shutdown signal" attribution follows the tree's own convention even though §29.4 step 13 cites §15.4.3/§28.5.3 for the `terminate` frame. Do not file that as a mis-citation; I checked and it is the house convention.

FILED (one finding): the reclaim hold's refusal predicate is stated three ways and only §15.4 carves `Shutdown` out. Design line 144 "admits no bind onto it"; §5.2 line 603 "admits no request that would create or resolve a registry entry under it"; §15.4 line 713 same predicate plus "`Shutdown` is not held". §5.2 is the normative home, so as staged an adapter must refuse a concurrent `Shutdown` with `ABORTED`, which §7.1 (line 390) then counts as a reclaim that did not complete and §6.2 turns into `leaked`.

WATCHOUT: several plausible-looking objections in this staging are already on the refuted list and are refuted for good reasons — the §5.2 "under no deadline" sentence, the `Slot cleanup:` bullet's unqualified reporting sentence, the unfenced-first-RPC bullet's "precedes any successor", and §15.4 not restating obligations §4.7 already carries. Do not re-derive them.
WATCHOUT: the "A bind that fails inside its first entry-creating RPC reclaims unfenced" bullet's TITLE says "entry-creating" while its BODY says "entry-touching". On the decomposed path the Launch connection's first entry-touching RPC is `StartSession`, which creates nothing, so the title under-describes the window. I judged this wording rather than a defect because the body is the operative text and the residue is accepted either way. EVIDENCE: spec-changes.md:196-203; pkg/gateway/podlifecycle/podsession/binder.go:764-769.


### [spec-recheck.2.review-client-surface.1]

DECISION: returned an empty findings list — BECAUSE every client-facing surface the staged spec
edits touch has a mirror staged for it, and I re-derived each mirror from the tree rather than
from the proposal. ALTERNATIVES: I considered filing the `SLOT_RECLAIM_OUTCOME_UNSPECIFIED`
meaning gap, the protocol-version question, and the §5.2-vs-§15.4 `Shutdown`-carve-out reading;
all three are recorded below as sub-bar.

FACT: `diff -rq` between this lane's r2 snapshot and the proposal directory shows ONLY the
review log changed. The spec staging is byte-identical to what round 1 of this recheck read, so
"the delta this loop exists for" is empty in `*.spec-changes.md`. — EVIDENCE:
scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r2

FACT: every SCHEMA-1 field number is free on its message, verified by dumping the message bodies
from the tree. `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (`reserved "slot_id"`), so 7 is
next; `ShutdownResponse` holds 1,2; `PrepareWorkspaceResponse` 1,2; `FinalizeWorkspaceResponse` 1;
`ResumeResponse` 1,2,3; `ConfigureWorkspaceResponse` 1; `RunSetupResponse` 1; `StartSessionResponse` 1;
`AssignCredentialsResponse` is `{}`. — EVIDENCE: schemas/lenny-adapter.proto:1033 and the
`ShutdownRequest` block around :1625-1636.

FACT: the adapter proto has NO SDK mirror. `grep -rl "adapterv1|lenny-adapter|bind_epoch|ShutdownRequest" sdks/`
returns nothing, and the only generated package is `pkg/proto/adapter/v1`. A client-surface sweep
does not need to check sdks/ for this proto. — EVIDENCE: sdks/ (client, runtime only)

FACT: §29.4 step 13's appended sentence is accurate about the wire. The `terminate` frame's schema
is `{type, deadlineMs, reason}` with `additionalProperties: false` and no session field, so
"pod-global and names no session" is verified against the published artifact rather than inferred.
— EVIDENCE: schemas/runtime-ops-events.schema.json:174-185

FACT: §4.7.1's §7.4 claim is true in the tree. The mid-session upload handler calls
`bind.Adapter.PrepareWorkspace` then `bind.Adapter.FinalizeWorkspace` on the session's existing
binding. — EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:128,:134

FACT: §4.7.1's caller rule that `DemoteSDK` removes the entry is true in the tree: the handler
calls `s.noteRuntimeClosed(sessionID)` then `s.releaseSessionSlot(sessionID)` on
`anyRegisteredSession()`. — EVIDENCE: pkg/adapter/sdkwarm.go:274-303

FACT: all seven RPCs §4.7.1 names as the bind sequence are rows in the §4.7 Gateway→Adapter table
(`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `StartSession`, `ConfigureWorkspace`,
`AssignCredentials`, `Resume`), so the block's "Every other RPC on this contract" resolves inside
the tables it sits under. — EVIDENCE: spec/04_system-components.md:666-678

FACT: the proposal's three §28 claims all check out. `grep -n Shutdown spec/28_communication-channels.md`
returns nothing; §28.5.1's cards are per channel (CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER,
CH-PODHEALTH) and name no message field sets; §28.7's wire-contract artifact register is one row per
`schemas/` FILE, so a new field on an existing message adds no row; §28.6 says of itself "It states no
constraint the cards do not carry". — EVIDENCE: spec/28_communication-channels.md:205-236, :1663-1670,
:1780-1782

FACT: §15.4.6's conformance categories all drive the RUNTIME BINARY over JSONL against a fake
adapter (stdin/stdout framing, message/response, heartbeat, MessagePart, MCP nonce, CH-RUNTIMEOPS),
so the proposal's reason for leaving them untouched is verified rather than asserted. §24.8's
external-adapter compliance suite is schema-driven ("assertions are generated from the published
`schemas/lenny-adapter.proto`"), so it absorbs additive proto fields with no spec edit. — EVIDENCE:
spec/15_external-api-surface.md:2062-2076; spec/24_lenny-ctl-command-reference.md:114

WATCHOUT: `docs/api/internal.md:75-128` carries a hand-written protobuf excerpt of `RuntimeAdapter`
that is WHOLESALE stale already — it declares `StopSession`, `UploadFiles`, and a
`StartSessionResponse {bool success; string error_code; string error_message}` that shares no field
with the shipped message. Do not file it as an edit site this proposal misses: it is not made newly
wrong by SCHEMA-1, and a fix is a pre-existing docs debt with no tie to this change. — EVIDENCE:
docs/api/internal.md:79,:123-127 versus schemas/lenny-adapter.proto `StartSessionResponse`

WATCHOUT: spec/04_system-components.md:1538 says "The `leaseToken` field in
`AssignCredentialsResponse` is unchanged", but that message is `{}` in the shipped proto. SCHEMA-1
adds `bind_epoch = 1` to it, so a reviewer meeting the sentence next to the SCHEMA-1 table will
think this proposal falsified it. It was already false before the edit and the edit adds no
`leaseToken`, so it is pre-existing and out of this proposal's scope. — EVIDENCE:
spec/04_system-components.md:1538; schemas/lenny-adapter.proto:1033

WATCHOUT: §15.4 states "Current protocol version: `1.0.0`. Major version changes are breaking;
minor/patch are backwards compatible." Requiring a third-party adapter to report `bind_epoch` and
to honour the hold is a semantic conformance break at an unchanged version string. I did NOT file
it: the platform is pre-deployment and carries no compatibility shims, so a complete deliberate
break is not a finding, and the version string is not part of any staged edit. A later reviewer who
wants it needs an argument that survives the pre-deployment rule. — EVIDENCE:
spec/15_external-api-surface.md:1699

WATCHOUT: SPEC-3's §5.2 hold says the adapter "admits no request that would create or resolve a
registry entry under it", with no carve-out, while SPEC-5's §15.4 hold block adds "`Shutdown` is not
held". I did NOT file the pair as a contradiction: by the time the hold exists the entry is already
deregistered, so a `Shutdown` in that window resolves nothing and the §15.4 sentence is the wire-level
disambiguation rather than an exception to a rule. If a later round files it, the argument has to be
about an adapter author who acts differently on the two readings, not about the wording. — EVIDENCE:
0081_...spec-changes.md:602 (§5.2 hold) and :714 (§15.4 "`Shutdown` is not held")

USEFUL [Settled: "No per-slot sub-state is client-visible"]: saved me a sweep of
pkg/api/v1/session, the OpenAPI document and the CRDs for the SPEC-4 edge; I spot-checked the SDK
arm instead and it held.


### [spec-recheck.2.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE `0081_....spec-changes.md` is byte-identical to the round-2 snapshot, so this lane's staged spec text did not change at all this round, and the only file that differs is the review log — ALTERNATIVES: re-walking the whole staging under the lens anyway (done, see below) and re-filing the shared-entry residue; rejected for the same reason `spec-recheck.1.review-docs-alignment.1` rejected it.

FACT: `diff -rq scratchpad/cp-snap/0081_.../spec-recheck-r2 proposals/0081_...` reports exactly one differing file, `...review-log.md`. `diff -u` on the two `spec-changes.md` copies is empty. A future agent on this lane should run that diff FIRST: the orchestrator brief says "that text is the only text here that no reviewer in this lane has read", which was not true this round, and knowing it up front saves a full re-derivation.

USEFUL [spec-recheck.1.review-docs-alignment.1]: its WATCHOUT ("the docs-alignment lens is nearly inert on THIS loop by construction … the only in-scope shapes are (i) an accepted failure mode with no landing spec sentence and (ii) a spec-internal inconsistency") is exactly right and is what let me scope the pass to the `## Edge cases and accepted failure modes` list plus the staged blocks, rather than re-walking `docs/`.

FACT: I walked all eleven rows of `## Edge cases and accepted failure modes` (spec-changes.md:157-270) against the staged spec text. Rows resolving to landing spec sentences: the upload-free no-op row → §4.7 row's "A request naming a session the adapter holds no entry for removes nothing" (:322); the unacknowledged-reclaim rows (concurrent and exclusive) → staged §7.1 (:390) and SPEC-3's §5.2 append (:600); the hold-refusal row → staged §15.4 `ABORTED` block (:713); the connect-stage row → SPEC-4's "No edge is added out of `slot_assigned`" note (:653); the co-tenant signal row → §4.7 row and §29.4 step 13 (:352). Rows whose observable outcome lands in NO staged spec sentence: the shared-entry teardown (:183-188, :231-247), residue class one via the start/reclaim race (:214-231) and via the late `StartSession` (:248-260), and the unfenced first-RPC window (:203-210). Not filed: same family, twice refuted on evidence in this loop, and the remedy in each case is a prescriptive-completeness sentence rather than a correctness fix.

WATCHOUT: residue class one ("the pod is left holding an entry and a runtime session no gateway attempt owns") is NOT inert on the adapter side, which is the strongest form the unfiled candidate could take and is worth knowing before someone re-derives it. A surviving orphan entry raises `len(s.slots)`, and `deliverToSession` rejects every unaddressed session-scoped runtime frame once `slotCount() > 1` — EVIDENCE: pkg/adapter/attach.go:345-364, pkg/adapter/slotsession.go:404-407. So a co-tenant on the same pod can start losing unaddressed frames because of a residue the gateway recorded as a clean `reclaimed`. Anyone who wants to file this must argue it as a §28.5.3 consequence with a landing spec sentence, not as documentation completeness; filing it in the shape the two earlier refutations killed will be killed again.

FACT: the adapter enforces no `maxConcurrentSessions` capacity check of its own — the only two consumers of the registry's cardinality are `claimPodMCPStartLocked` (`len(s.slots) != 1`, pkg/adapter/slotsession.go:110) and `slotCount()` for §28.5.3. So the natural "an orphan entry permanently consumes a slot of the pod's capacity" argument does NOT hold on the adapter side; capacity is gateway-side accounting. Do not build a finding on it.


### [spec-recheck.2.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens — BECAUSE the spec-changes.md delta since the r4 snapshot is a single bullet (the "A retry that meets the reclaim hold spends an attempt on it" edge case, spec-changes.md:190-193), both of its new claims verify, and a fresh identifier-by-identifier sweep of spec/, docs/, schemas/ and charts/ turned up no surface the staged edits falsify that the edit lists omit — ALTERNATIVES: see the rejected candidates below.

FACT: `diff -u` between the spec-r4 snapshot and the live proposal shows spec-changes.md changed in exactly ONE hunk; `diff` against the spec-recheck-r2 snapshot shows spec-changes.md UNCHANGED (only review-log.md differs). So the r2 snapshot the orchestrator names is NOT the right baseline for this lane's delta — use spec-r4. EVIDENCE: scratchpad/cp-snap/0081_.../spec-r4 vs spec-recheck-r2.

FACT: the delta's two new claims both check out. §15.4's staged reclaim-hold block does publish the code — "refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on" (spec-changes.md:713) — and the `awaiting_client_action` claim matches the staged `isTransientPodClaimError` `codes.Aborted` arm (implementation-checklist.md:30, non-spec-changes.md:917).

FACT (edit-list completeness, re-derived this round): the "Spec files touched" list is exhaustive against the five deliverables' headings — spec/04 (§4.1, §4.7 `Shutdown` row, §4.7.1, §4.7.9 step 5), spec/05 (§5.2 scrub model, `**Slot cleanup:**`, `**Max retries:**`), spec/06 (fence, post-fence prose, `resuming` cancel bullet), spec/07 (§7.1, §7.2, §7.3), spec/15 (§15.4), spec/29 (§29.4 step 13). No heading in the staging is missing from the list and no list entry lacks a heading.

FACT: `grep -rn "Shutdown" spec/*.md` outside spec/04 hits only spec/05:459 (recycle-disposition trigger), spec/11:263,:270 (§11.4 revoke fan-out), spec/15:1797 (a JSONL drain note, unrelated), spec/29:696 (step 12) and spec/07:50 (the fenced flow line). Each sends the epochless form, which the staged §4.7 row keeps defined, so none needs an edit. Likewise `grep -n "Shutdown\|StartSession\|AssignCredentials"` over spec/28 returns only §28.5.1 prose about `CH-RUNTIMEOPS`, which confirms the "**§28's registers**" untouched-claim.

FACT: §15.4.2's RPC lifecycle state machine (spec/15:1686-1706) is a coarse INIT/READY/ACTIVE/DRAINING/TERMINATED process machine with no per-RPC error or status-code inventory, and the §4.7.3 table at spec/04:704-716 is CH-ADAPTEREVENTS events rather than gRPC statuses. Neither owes a row for the `ABORTED` refusal. Do not re-derive.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule citing it is accurate: the handler calls `s.releaseSessionSlot(sessionID)` synchronously inside the RPC. EVIDENCE: pkg/adapter/sdkwarm.go, `func (s *Server) DemoteSDK` body.

MISTAKE (nearly filed, do not re-derive): "spec/04:1538 says `AssignCredentialsResponse` carries a `leaseToken` field, and SCHEMA-1 makes `bind_epoch` field 1 of that message, so the two disagree." The message is `message AssignCredentialsResponse {}` at schemas/lenny-adapter.proto:1033 — spec/04:1538 is ALREADY false before this proposal, and adding a field neither creates nor worsens it. Pre-existing.

MISTAKE (nearly filed): "spec/15:1697 pins the adapter protocol version at `1.0.0` and nine added proto fields need a minor bump, so §15.4.2 is an unstaged edit site." Nothing in the spec obliges a bump on an additive change (it states only that major is breaking and minor/patch are compatible), and `.claude/rules/code-best-practices.md` bars backward-compatibility handling outright because the platform is pre-deployment, so a mixed-version gateway/adapter pair is not a case the spec has to cover. No agent had looked at this before (`grep -n "protocol version\|1\.0\.0"` over the proposal directory returns nothing), so record it as looked-at rather than as open.

MISTAKE (nearly filed): "docs/api/internal.md:75-135 publishes a `RuntimeAdapter` proto excerpt that SCHEMA-1 falsifies and no deliverable edits it." That excerpt already names RPCs that do not exist (`StopSession`, `UploadFiles`) and a `StartSessionRequest` field set unrelated to the shipped one; it is wholesale stale, and review-log.md:645 already rules the internal.md tables out of scope. Also a docs remedy, hence not this loop's.

USEFUL [review-log.md:528, :384]: the standing Trap barring the `slot_cleanup ──→ leaked` "(cleanup timeout exceeded)" gloss saved a filing — spec/06:148's gloss is already narrower than spec/05:545's "If cleanup fails, the slot is leaked" before this proposal applies.

USEFUL [review-log.md Settled, "the anchor sweep is done ... SIXTEEN sites"]: I did not re-run the verbatim-anchor sweep on the strength of that entry, and the entry names each site, so a later reviewer can check one anchor cheaply instead of all of them.


### [spec-recheck.2.review-feasibility.1]

FACT: This round's snapshot diff is EMPTY for the spec staging. `diff -rq scratchpad/cp-snap/.../spec-recheck-r2 proposals/0081_...` reports exactly one differing file, the review log (compaction pass 11). `0081_....spec-changes.md` is byte-identical to the r2 snapshot, so the "delta to look at first" for this firing was review-log compaction only. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log.md:5

DECISION: Returned an empty findings list under the actor-action-feasibility lens — BECAUSE every actor assignment in the staged spec text resolves to a component that exists under that name and can see the data its check needs, and the one family that looked infeasible is an already-refuted close variant. ALTERNATIVES: filing the Launch-reconnect no-connection case (see WATCHOUT below) and filing §15.4's unobservable `PrepareWorkspace` conformance clause; both rejected, the first as a refuted variant with a verified premise, the second as over-specification/no-harm.

WATCHOUT: The "§7.1 obliges a reclaim where the gateway holds no adapter connection" family is REFUTED and its factual premise is now independently re-verified, so do not re-derive it. `Binder.Prepare` closes its connection on success (pkg/gateway/podlifecycle/podsession/binder.go:957 `cl.Close()`) and `Binder.Launch` re-dials (binder.go:1115 `reconnect`), so a Launch reconnect failure leaves a bound entry on the pod with no connection to carry the reclaim and §7.1/§4.7.1 forbid dialling. That ordering is exclusive-pool only: `prepareAtFinalize` returns `(nil, nil)` when `match.MaxConcurrentSessions > 1`, so the split never runs on a concurrent pool, and §7.1's own closing sentence retires the pod there. — EVIDENCE: pkg/gateway/sessionserver/finalize.go:238-240 `if match.ExecutionMode == string(runtimestore.ExecutionModeService) || match.MaxConcurrentSessions > 1 { return nil, nil }`

FACT: All seven RPCs §4.7.1 names as the bind sequence exist in `schemas/lenny-adapter.proto` (lines 41, 48, 55, 62, 72, 89, 151), all have unary responses (`PrepareWorkspace` is client-streaming with one response), all appear as rows in the §4.7 Gateway→Adapter table (spec/04_system-components.md:666-681), and all four staging/creds handlers really do create-or-resolve the registry entry through `ensureSlotPaths` (pkg/adapter/staging.go:134, :181, :337; pkg/adapter/slot.go:135-148). `PrepareWorkspace` resolves it exactly once, guarded by `if stagingDir == ""` (pkg/adapter/staging.go:75-83), which is what makes §4.7.1's one-epoch-per-call rule implementable.

FACT: `DemoteSDK` really does remove the registry entry inside the RPC (`s.anyRegisteredSession()` → `noteRuntimeClosed` → `releaseSessionSlot`, pkg/adapter/sdkwarm.go:294-297), which grounds both §4.7.1's caller rule ("it holds none once it has itself issued an RPC ... that removes the entry") and §5.2's "a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers".

FACT: §5.2's "for a hold taken outside any request, by the termination window of the pass that runs the cleanup" has a real referent: `terminateHeldSession` (the §10.1.4 coordinator-lost pass) closes the runtime and calls `removeSlotTree` with no inbound RPC. — EVIDENCE: pkg/adapter/holdstate.go:229-254

FACT: `ABORTED` appears nowhere in `spec/` today (`grep -rn "ABORTED\|Aborted" spec/*.md` is empty), so SPEC-5's §15.4 block is the first spec statement of it and cannot contradict an existing code-to-category mapping in the spec.

FACT: `Shutdown` appears in no `spec/28_communication-channels.md` row (grep returns nothing), so the "§28's registers" line in "Spec sections deliberately untouched" is accurate.

FACT (pre-existing, NOT this proposal's): spec/04_system-components.md:1538 says "The `leaseToken` field in `AssignCredentialsResponse` is unchanged", but `AssignCredentialsResponse` is an empty message (schemas/lenny-adapter.proto:1033). SCHEMA-1 putting `bind_epoch = 1` there neither creates nor worsens that error, so it is not a missed edit site for 0081. Recorded so the next reviewer does not spend the same twenty minutes on it.

UNVERIFIED: §15.4's non-conformance clause "resolves the slot identifier more than once within one `PrepareWorkspace` call" is not observable on the wire (the call has one response carrying one epoch), so CONF-1 cannot assert it directly. I judged this below the bar (no harm, no false citation). Someone reviewing CONF-1's test list should confirm the tier-10 case it maps to is the observable sibling clause rather than this one.


### [spec-recheck.2.review-fresh.1]

DECISION: returned an EMPTY findings list — BECAUSE `spec-changes.md` is byte-identical to the
`spec-recheck-r2` snapshot (`diff -rq` reports only `review-log.md` differing), so this round had no
delta at all in the file this loop owns; and the only delta since `spec-r4` is the one four-line hunk
in the "A retry that meets the reclaim hold spends an attempt on it" bullet, whose two new claims
(the `ABORTED` status §15.4 publishes; the §7.3 `awaiting_client_action` hold for the client's own
retry) both check out against spec/15 and spec/07:411-414. ALTERNATIVES developed and rejected below.

FACT: `diff -ru scratchpad/cp-snap/.../spec-recheck-r2 proposals/0081_.../` returns ONLY
`review-log.md`. A recheck round can legitimately have a zero-byte staging delta; do not spend the
round hunting for a hunk that is not there. — EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r2/

MISTAKE (nearly filed, do NOT re-derive): "§5.2's reclaim-hold paragraph puts *the close of the
session on the pod's shared runtime process* inside 'the cleanup', while the §4.7 row splits the
runtime teardown out of the §5.2 slot cleanup and SPEC-3's own `**Slot cleanup:**` action list
(workspace dir, process group, credential dir, §4.9 timers, `slotId`) omits the close — so the hold
would end before the runtime close and a successor could bind onto a runtime the reclaim is still
closing." It does not, and the killer is an ordering the same paragraph states: "The cleanup's
removal of the slot's directories runs **after that close**". The directory removal is unambiguously
inside every reading of "the cleanup", so the hold transitively covers the close under either
extent of the word. There is no harm, only a two-scope use of "cleanup".
— EVIDENCE: spec-changes.md:603 (hold paragraph), :575 (widened action list), :322 (§4.7 two-teardown split).

FACT: every anchor I re-verified independently this round resolves and is unique in the tree:
spec/04:157 (§4.1 third sentence), spec/04:686 (`Shutdown` row opening, `count()==1`),
spec/04:659/:688/:695 (the §4.7.1 insertion window), spec/05:453/:545/:555, spec/06:152-153/:156/:234
(`count()==1`), spec/07:23/:24/:210/:213/:214/:414, spec/15:1469/:1471, spec/29:704-711.
The `#471-role-and-gateway-rpc-contract` anchor SPEC-1/SPEC-2/SPEC-5 mint is already in use at
spec/README.md:28. Citations `schemas/lenny-adapter.proto:1630-1635`, `spec/04:151`, `spec/04:157`,
`spec/29:586-588`, `:669-674`, `:697` are all exact.

FACT: the four "deliberately untouched" claims I sampled hold. `grep -n Shutdown spec/28_*.md`
returns nothing, so the §28-registers row is right; spec/05:455 does say "after every ended session's
per-slot tree and credential lease have been removed", which is SPEC-3's stated justification for
widening the action list; `/run/lenny/slots/{sessionId}/credentials.json` is the corpus spelling; and
the proto has seven distinct bind-sequence response messages, matching §15.4's "seven bind-sequence
responses". — EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151; spec/05:455.

WATCHOUT: the §6.2 "Pre-`running` slot cleanup" prose enumerates the stages that leave a slot in
`receiving_uploads` by reference to the §4.7.9 step-5 sequence alone, which is the `type: agent`
pod-warm list and does not name `ConfigureWorkspace` (the SDK-warm bind). I judged this NOT a
finding because the fence edge's own annotation is generic ("the bind is abandoned or fails before
the runtime has been given the session") and covers it, and because the standing DECISION at
review-log.md:~50 deliberately refuses to enumerate the three start RPCs at six sites. A later lens
tempted by it should start from that reading rather than re-derive it.
— EVIDENCE: spec-changes.md:666 (§6.2 prose), :655 (fence edge annotation).

USEFUL [review-log.md "Standing context" Settled block]: the anchor sweep entry and the
"`Shutdown` is unfenced" / "`ShutdownResponse` is `{exited_cleanly, exit_code}`" entries saved me
from re-deriving the §10.1 fence interaction and the proto-field question from scratch.


### [spec-recheck.2.review-kubernetes.1]

FACT: The spec-changes.md file did NOT change between the `spec-recheck-r2` snapshot and this
round — `diff -u scratchpad/cp-snap/.../spec-recheck-r2/...spec-changes.md
proposals/.../...spec-changes.md` is empty; the only differing file in that snapshot pair is
the review log. So this round's "delta" for the spec lane is nil and the recheck is a
full re-read. EVIDENCE: diff -rq scratchpad/cp-snap/0081_.../spec-recheck-r2
proposals/0081_... reports only `...review-log.md differ`.

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE the staged
spec edits touch no CRD subresource, no field-manager ownership, no finalizer, no admission
webhook predicate, no controller reconcile, and no watch. Every actor named in the staged
text is the gateway (in-process, over the adapter gRPC connection) or the adapter (pod-local
registry, filesystem, process group). ALTERNATIVES: I considered filing on (a) the §6.2
`resuming → cancelled` bullet gaining an adapter RPC, and (b) the `leaked` disposition
holding a `SandboxClaim` in `bound`. Both are clean: spec/06_warm-pod-model.md:231 says "The
gateway applies the following transitions", so the bullet's actor is already the gateway
rather than a controller; and the standing context already records the §4.6.1 orphan-GC
backstop for the `bound` claim.

FACT: `lenny_adapter_leaked_slots` is specified as an ADAPTER-exposed gauge (`/healthz`
response, labeled by `pod_id` and `pool`) at spec/06_warm-pod-model.md:160, while the
standing context records the shipped emitter as the gateway
(`gatewaymetrics_credential.go:219-223`). SPEC-3's appended text says a non-completing
pre-`running` cleanup "is surfaced on the `lenny_adapter_leaked_slots` gauge" and names no
emitter, so it is compatible with either. I did not file this: the emitter mismatch predates
the proposal and the staged sentence adds no attribution.

UNVERIFIED: on the pre-`running` path there is no runtime to close, so a shipped adapter's
`exited_cleanly` (derived from the runtime close alone, tree-removal error discarded — see
Settled) would report clean even when `removeSlotTree` failed, which would make SPEC-3's
"does not report a clean exit" trigger unreachable for a tree-removal failure. That is a
mechanism/code-lane question rather than a Kubernetes-idiom one; the mechanism lens or the
non-spec lane should confirm CODE-1 widens `ExitedCleanly` to cover the tree removal.


### [spec-recheck.2.review-mechanism.1]

FACT: the spec-changes.md file did NOT change between spec-recheck-r2 and this round. `diff -rq` over the whole proposal directory reports only the review log differing. So this round's "delta" for the spec lane is empty and the whole staging had to be re-read cold. EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r2 vs proposals/0081_... (diff -rq, one file)

DECISION: filed exactly one finding — the `receiving_uploads → running` boundary — BECAUSE the standing context itself carries an explicit UNVERIFIED asking a later reviewer to confirm the four `running`-boundary sites agree, and they do not: three staged sites (SPEC-4 fence annotation, SPEC-4 prose, Design) put a start still in flight on the `receiving_uploads` side, while the UNEDITED trigger annotation at spec/06_warm-pod-model.md:152-153 reads "session dispatched to runtime with its session identifier", which is satisfied by an in-flight start. ALTERNATIVES: rejected the two §5.2-vs-§15.4 hold-predicate angles and the `slot_cleanup ──→ leaked` gloss angle, both already adjudicated (review-log.md:528, :3905, :3952).

WATCHOUT: `§5.2 says the hold refuses "any request that would create or resolve a registry entry" while §15.4 carves `Shutdown` out` is NOT a contradiction and has been refuted at least six times. The reason it is not: during the hold the entry has already been deregistered, so a `Shutdown` neither creates nor resolves one; §15.4's carve-out is disambiguation. Do not re-derive. EVIDENCE: review-log.md:3905 lists four prior adjudications.

FACT: the recycle-`Shutdown`-meets-the-hold harm does not exist. `Binder.ReleaseSlot` sends the ending session's `Shutdown` synchronously and only then the recycle `Shutdown` on the same connection, and the adapter's release runs its whole cleanup inside the requesting RPC (`releaseSessionSlot` → `deregisterSlot` then `removeSlotTree`, both synchronous), so the hold is released before the first `Shutdown` answers. This is what SPEC-3's "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" is buying. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:541-577; pkg/adapter/slotsession.go:214-219.

FACT: the seven bind-sequence RPCs in SPEC-5 are exactly the entry-creating set in the tree. `ensureSlotStateLocked` has three callers reachable from RPCs: `ensureSlotPaths` (PrepareWorkspace via `resolvePrepareStagingDir`, FinalizeWorkspace, RunSetup), `assignCredentialsSlot` (AssignCredentials), and `claimSessionSlotUnderLock` (StartSession, Resume, SDK-warm ConfigureWorkspace). No eighth. EVIDENCE: pkg/adapter/slot.go:105,140; staging.go:134,181,337; credentials.go:74; slotsession.go:53,75.

FACT: `PrepareWorkspace` resolves the slot exactly once per stream (`if stagingDir == ""`), so SPEC-5's "the adapter resolves the entry once, at the frame from which it first resolves the slot identifier" is true of the shipped handler. EVIDENCE: pkg/adapter/staging.go:75-83.

FACT: the gateway dials a fresh adapter client per bind (`DialAdapter` under `connectSlot`/`connect`), so §4.7.1's per-CONNECTION epoch latch is effectively per-session and cannot cross-name a co-tenant's epoch. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:59,1737; slotbinder.go:415.

FACT: `ShutdownRequest` fields 1,2,3,5,6 are used and 4 is reserved (`slot_id`), so `expected_bind_epoch` at field 7 is free and `coordination_generation` is a bare `int64` with no wire presence, which is what makes SPEC-1's §4.1 argument hold. EVIDENCE: schemas/lenny-adapter.proto:1609-1636.


### [spec-recheck.2.review-operational.1]

DECISION: empty findings list for the operational-consistency lens on the spec staging, second consecutive empty return for this lens — BECAUSE `spec-changes.md` is BYTE-IDENTICAL to the `spec-recheck-r2` snapshot (the only file that changed in this window is the review log's compaction pass 11), and a full re-sweep of every observability surface the staging touches came back clean. ALTERNATIVES rejected, each with what killed it: (a) "§5.2's `concurrent_slots_exhausted` definition becomes wrong once a pod is excluded for holding an incomplete reclaim" — the divergence is PRE-EXISTING, see the FACT below; (b) "§5.2:561's `leaked` gloss `(cleanup timeout exceeded)` and §6.2:148's identical edge annotation are narrower than the new producer" — also pre-existing, today's only shipped leak producer (`ReleaseSlotReservation` erroring in `applySlotRetryPolicy`) is not a cleanup timeout either; (c) "the hold-refused §7.3 resume emits no `lenny_slot_failure_total`, so §5.2's 'accounted by the gateway as an ordinary transient slot failure' is false there" — `Binder.Resume` indeed never calls `recordSlotFailure`, but CODE-5 routes the resume branch into `accountSlotFailure`, so the health-ledger accounting the sentence claims does land; the residual metric-label question is already the standing OPEN at review-log.md:778 and belongs to the non-spec lane.

FACT: the delta this loop was launched for does not exist in this lane. `diff -u scratchpad/cp-snap/.../spec-recheck-r2/0081_....spec-changes.md proposals/.../0081_....spec-changes.md` produces NO output; the only changed file in the whole proposal directory is `...review-log.md`. Do not spend a round hunting a spec delta. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md (761 lines, unchanged)

FACT (pre-existing, so not filed, but re-derived from scratch this round): `details.reason: "concurrent_slots_exhausted"` is ALREADY returned for a pod that has a free slot but was skipped by a read-only placement filter. `expiredByUptime` is exactly such a filter and it `continue`s past the pod in both candidate passes, after which the function returns `ErrNoConcurrentSlot`. §5.2's definition ("pods exist but all slots are full") is therefore already imprecise before SPEC-2's `ExcludePods` skip widens it, which is why the reuse SPEC-2 asserts at spec-changes.md:542-547 does not make the definition newly wrong. A future reviewer tempted to file this must argue from a harm the `expiredByUptime` precedent does not already carry. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:433-440,:491-497,:508-522; spec/05_runtime-registry-and-pool-model.md:549

FACT: `Binder.Resume` has no `recordSlotFailure` call on any of its error branches; the four `lenny_slot_failure_total` emission sites all live in `materializeSlot`, which only `BindSlot` and `BindReservedSlot` reach. So the create-time-reserved path DOES emit the counter and the §7.3 re-attach path does NOT. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1662; pkg/gateway/podlifecycle/podsession/slotbinder.go:128-133,:210-254,:287,:294,:302,:309,:322

FACT: `ABORTED` appears NOWHERE in `spec/` today (`grep -rn "ABORTED\|Aborted" spec/*.md` is empty), so SPEC-5's §15.4 reclaim-hold block is the first spec statement of a gRPC status code on this contract and can collide with no existing classification table. §15.4 has no per-RPC error-code table; its only prior code is `UNIMPLEMENTED` in the SDK-warm demotion paragraph. EVIDENCE: spec/15_external-api-surface.md:1469; spec/15_external-api-surface.md:1686-1707 (§15.4.2 is a five-state process machine with no error rows)

FACT: SPEC-3's "counts toward the whole-pod replacement trigger stated below" resolves. The scrub-model anchor is spec/05:453 and the `**Whole-pod replacement trigger:**` bullet is spec/05:561, so "below" is literally true within §5.2. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,:561

USEFUL [Standing context: "No alert anywhere references any slot metric"; "spec/16 needs no edit"; "Dead end: the adapter cannot see this leak"]: all three held on an independent re-derivation. `grep -rn "slot" pkg/alerting/rules/*.go` returns nothing, spec/16 carries only `lenny_slot_failure_total` and `lenny_slot_pod_replacement_total` and the staging touches neither, and the staging mints no metric, no alert, no runbook and no CRD condition. The alert/runbook/condition half of this lens is inert on the WHOLE staging rather than only on a delta, which is why two consecutive empty returns are the expected result and this lens can be retired.


### [spec-recheck.2.review-performance.1]

FACT: the spec staging did NOT change in this round. `diff -rq scratchpad/cp-snap/0081_.../spec-recheck-r2 proposals/0081_...` reports exactly one differing file, the review log; the spec-changes file is byte-identical. This is now the FOURTH consecutive round in which a performance lens has been re-run over unchanged staged spec text (see review-log.md:1444 `[spec.2.review-performance.1]`, :2021 `[spec.3.review-performance.1]`, :3969 `[spec-recheck.1.review-performance.1]`). — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md (unchanged, 761 lines)

DECISION: returned an empty findings list — BECAUSE I re-derived the four capacity grounds against the tree rather than trusting the standing entries, and each holds. (1) No control-plane or data-plane write is added: the epoch is an in-memory `int64` per registry entry, explicitly pod-local and never persisted (spec-changes.md:678-679), and the hold is an in-memory identifier key (spec-changes.md:601-604). No etcd status write, no Postgres row, no Redis key, no informer, no net-new watch. (2) The only new per-unit-of-work traffic is one compensating `Shutdown` per FAILED bind plus one `int64` on seven bind-sequence responses, so the amplification factor multiplies against the bind-FAILURE rate rather than the request rate. (3) `ReportSessionScrub` volume goes DOWN under SPEC-3, which withholds the report on the pre-`running` path (spec-changes.md:596-600). (4) No serialization bottleneck: the hold is keyed on the slot identifier and `slotId == sessionId` (spec/05_runtime-registry-and-pool-model.md:395), so N concurrent cleanups on one pod hold N distinct keys and collide only with a retry of the same session. Failover posture: nothing staged reads or writes Postgres; the `leaked` occupancy stays in the shipped Redis slot counter under §6.2's own disposition; §10.1's handoff is a stated non-impact because the epoch is pod-local and latched per connection, and a replica lost mid-bind sends no compensation, which is exactly today's behaviour. — ALTERNATIVES: I considered filing (a) an ABORTED-refusal retry storm at the top tier, dropped because the refusal is bounded by the caller's own §5.2 retry budget and the proposal states the cost as an accepted edge case (spec-changes.md:190-200); (b) the "under no deadline" hold disclaimer, dropped as an already-refuted close variant recorded in the prompt's refuted list.

WATCHOUT: this lens is structurally inert on this staging and has now returned empty four times over byte-identical text. If a future round re-runs it with the spec staging still unchanged, the honest answer is empty again; the residual capacity questions the log carries (correlated re-attach churn at Tier 3, the exclusive-pool resume budget clamp, mass pod retirement on a fleet-wide transport blip) all have their remedy in the NON-SPEC lane or in a human decision, not in staged spec text. — EVIDENCE: review-log.md:773, :798, :845


### [spec-recheck.2.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE `spec-changes.md` is BYTE-IDENTICAL to the
`spec-recheck-r2` snapshot (`diff -u` between the two returns nothing; only `review-log.md`
differs), so this lane's staging carries no delta at all this round, and every reliability
candidate I built against it is either already adjudicated or dies on the tree.
ALTERNATIVES rejected, with why: (a) adapter-process-restart ABA on the epoch (epochs restart at
1, a lagging `Shutdown` naming epoch N could match a fresh entry at N) — dead, because §7.1 and
§4.7.1 both forbid the compensation from re-dialling and an adapter restart kills the connection
the epoch was latched on, so no reclaim can cross a restart; (b) the compensation having no
deadline once `context.WithoutCancel` strips one — dead, `non-spec-changes.md:553-555` wraps it in
`context.WithTimeout(context.WithoutCancel(ctx), slotCleanupBudget(...))`; (c) §5.2's blanket hold
predicate versus §15.4's "`Shutdown` is not held" — already declined six-plus times
(review-log.md:782, :1608, :1940, :3159, :3905, :3937; archive:16025); (d) a gateway replica dying
between the bind failure and the compensation leaves the entry with no reclaimer — pre-existing
(before this proposal nothing was sent at all), and §4.6.1 orphan-GC of a `bound` claim is the
backstop, recorded in the standing context.

FACT: the delta since `spec-r4` is exactly one hunk, the "A retry that meets the reclaim hold
spends an attempt on it" bullet at spec-changes.md:190-193, and both of its new claims verify.
`ABORTED` is published as the transient classification by staged §15.4 (spec-changes.md:713), and
the §7.3 row-hold claim resolves against the shipped `holdOrFailOnResumeError` →
`isTransientPodClaimError` pair. — EVIDENCE: pkg/gateway/sessionserver/start.go:3609-3620,:3648-3680

FACT: there IS a real "hold taken outside any request" path in the tree, and staged §5.2's clause
"for a hold taken outside any request, by the termination window of the pass that runs the
cleanup" is accurate about it. §10.1.4's coordinator-loss self-termination clears
`s.hold.active` BEFORE it deregisters, so RPCs are admitted again while pass 2 is destroying
state; pass 1 is the locked deregistration, pass 2 shares one 10s `closeCtx` for the runtime
closes and then calls `removeSlotTree` with no deadline. Whether CODE-6 actually takes the
reclaim hold on this path is a CODE-lane question, not a spec one. — EVIDENCE:
pkg/adapter/holdstate.go:176-205 (`onHoldTimeout`, two passes, the 10s ctx), :254
(`removeSlotTree(m.state)`)

UNVERIFIED: staged §5.2's complementary arm "A cleanup that reclaims a slot the pod's shared
runtime process was given is a session release like any other and reports its outcome" is
violated by that same §10.1.4 path, which deregisters STARTED sessions and deliberately files no
`ReportSessionScrub` ("the scrub report is the record of a scrub a Shutdown teardown performed,
and this path performs none", pkg/adapter/holdstate.go:214-218). I did NOT file it, because
§5.2's SHIPPED scrub-model sentence already says a per-slot cleanup runs "on every session
release, reported by the adapter via `ReportSessionScrub`", so the divergence is pre-existing and
applying SPEC-3 does not change its truth value; spec/10 carries no carve-out (grep for "scrub"
in spec/10_gateway-internals.md returns nothing). Someone deciding whether §10.1.4 owes a
carve-out sentence should start from those two citations rather than re-deriving them.


### [spec-recheck.2.review-security.1]

DECISION: returned an empty findings list — BECAUSE the spec staging is BYTE-IDENTICAL to the
snapshot my lens's predecessor read and cleared, and every angle I rebuilt independently lands
on a recorded refutation, a pre-existing condition, or "merely less strict".
ALTERNATIVES rejected, with the evidence that killed each:
 (a) `superseded`/`absent` as an adapter self-report that suppresses the `leaked` disposition and
     the §5.2 pod-exclusion placement rule. Dead: the shipped `leaked = err != nil || !cleanly`
     already rests on the adapter's own `exited_cleanly`, so the new outcomes add no capability a
     lying adapter did not have. EVIDENCE: pkg/adapter/session.go:291 (`ExitedCleanly: closeErr == nil`);
     podsession/slotbinder.go:536-559.
 (b) The withheld `ReportSessionScrub` on the pre-`running` path not advancing
     `recycle.maxSessionsPerPod` for a bind that already ran `RunSetup` and wrote a credential
     file. Dead on the same ground `[spec-recheck.1.review-security.1]` recorded at
     review-log.md:4045: argued design plus the whole-pod scrub step-0 purge backstop.
 (c) SPEC-3 codifying §4.9 expiry-timer cancellation while `removeSlotTree` may have failed,
     leaving a real long-lived `anthropic_direct` key readable by every co-tenant slot
     (spec/13_security-model.md:30) with its only in-pod TTL enforcement cancelled
     (spec/04_system-components.md:1169). Dead as PRE-EXISTING: `deregisterSlotLocked` already
     cancels every armed timer unconditionally and `removeSlotTree`'s error is already discarded.
     The proposal's staged §5.2 action list codifies shipped behaviour and its ordering
     (remove dir, then cancel) is the safe one.

FACT: the pre-`running` leak accounting the staged §5.2 append relies on ("the adapter's
`Shutdown` response for that reclaim does not report a clean exit") is FALSE against the shipped
adapter, which sets `ExitedCleanly: closeErr == nil` and drops `removeSlotTree`'s error
(pkg/adapter/session.go:271,:291). CODE-1 repairs exactly that with
`ExitedCleanly: closeErr == nil && (live || treeErr == nil)` (non-spec-changes.md:252). The spec
assertion is therefore sound only WITH CODE-1. Anyone who moves or weakens that expression
falsifies a staged spec sentence and silently deletes the only accounting for residual
credential material on the pre-`running` path.

FACT: shipped `Shutdown` reaches `removeSlotTree` only under `bound := removed && st.sessionID != ""`,
so an entry that never reached `AssignCredentials` today leaves its `/workspace/slots/{sessionId}/`
tree on disk after teardown. SPEC-1's "runs whenever the adapter holds an entry ... whether or not
`AssignCredentials` has bound that entry" is a genuine tightening, not a restatement.
EVIDENCE: pkg/adapter/session.go:239-241,:271.

USEFUL [spec-recheck.1.review-security.1, review-log.md:4039-4049]: it had already verified
SPEC-3's two added cleanup actions against `slotlayout/tree.go:58-68` and `slotsession.go:174-188`
and against the `/run/lenny/slots/{sessionId}/credentials.json` path in spec/13:26. That saved a
full credential-path re-derivation and is the entry that turned my angle (c) into a pre-existing.

WATCHOUT: `diff -ru` against BOTH provided snapshots shows `...spec-changes.md` unchanged since
`spec-recheck-r2`; only `...review-log.md` moved. A security reviewer arriving at
`spec-recheck.3` on an unchanged staging has no new surface at all and should say so rather than
manufacture one. EVIDENCE: `diff -rq scratchpad/cp-snap/.../spec-recheck-r2 proposals/0081_...`
lists only the review log.

OPEN (unchanged, still nobody's): does the adapter's own `/healthz` `leaked_slots` count
(spec/06_warm-pod-model.md:160 makes it an ADAPTER health-metadata export) ever see a
pre-`running` leak, given the adapter withholds the report and only the gateway reads the
non-clean-exit answer? §6.2 asserts the gauge and the Redis leaked-occupancy are equivalent;
in code only the gateway emits it. Pre-existing spec/code divergence, twice declined as not this
proposal's defect. An observability or conformance lens should settle it.


### [spec-recheck.3.review-mechanism.1]

DECISION: returned an EMPTY findings list for the end-to-end-mechanism lens on the r3 spec staging — BECAUSE the round's whole delta is three wording swaps and each closes the contradiction it was written for without opening a new one, and every other mechanism angle I re-derived independently landed in the already-refuted set. ALTERNATIVES considered and dropped are listed below so the next mechanism lens does not re-walk them.

FACT: the r3 delta is exactly three hunks, all in spec-changes.md, all the same repair. (1) Design :21 and the SPEC-1 §4.7 row :322 change "rather than at the runtime's acknowledgement of it" to "rather than at the moment the session reaches the runtime". (2) Design :90-93 and (3) SPEC-4's §6.2 prose :660 drop the citation "which is the moment §6.2's `receiving_uploads → running` trigger names" and restate the boundary directly as "the pod's shared runtime process has been given the session with its session identifier", with "a start still in flight, whose session has not yet reached the runtime". `diff -ru scratchpad/cp-snap/.../spec-recheck-r2 proposals/0081_...` is the whole change; the spec-recheck-r3 snapshot is byte-identical to the working tree, so the r3 fixer made no edits.

FACT: the reworded `running` boundary is now consistent at the four sites the standing context's UNVERIFIED entry asked a later reviewer to confirm, and it does NOT reopen the earlier "contradicts the unedited trigger" finding. spec/06_warm-pod-model.md:152-153 reads "receiving_uploads ──→ running (workspace ready, session dispatched to runtime with its session identifier)"; the staged prose restates that as "given the session with its session identifier" without claiming the two are the same moment, and the §4.7 row's own "rather than at the moment the session reaches the runtime" fixes "given" == "reached", which is the later moment `runtimeLive` records. Design :22, §4.7 row :322, SPEC-4 fence annotation :643-645 and SPEC-4 prose :660 all agree. This closes the standing UNVERIFIED; do not re-derive it.

FACT: `PrepareWorkspace` resolves the slot exactly once, from the first frame that carries a session id, and sends one response at `SendAndClose`. §4.7.1's streaming clause ("the adapter resolves the entry once, at the frame from which it first resolves the slot identifier, and the call's single response reports that entry's epoch") is accurate against the tree. EVIDENCE: pkg/adapter/staging.go:75-83 (the `if stagingDir == ""` guard), :114-118 (`SendAndClose`). This retires the archive's UNVERIFIED at review-log-archive.md:15228.

FACT: the §5.2 reclaim hold's predicate ("admits no request that would create or resolve a registry entry under it") has a VACUOUS resolve arm for every RPC, not only for `Shutdown`. The hold starts at the critical section that deregisters the entry, so while it runs no entry exists and nothing can resolve one; the only reachable arm is creation, and the creation sites are exactly the bind sequence (`ensureSlotStateLocked` via `ensureSlotPaths` at pkg/adapter/staging.go:134,:181,:337, `claimSessionSlotUnderLock` at slotsession.go:75, `assignCredentialsSlot` at slotcreds.go:26). So the predicate cannot oblige an adapter to answer `ABORTED` to `Checkpoint`, `Interrupt`, `RotateCredentials` or any other entry-reading RPC. I nearly filed that as an unstaged behaviour change for the whole non-bind RPC surface and it dissolves on the hold's own start point — the same reasoning that already refuted the `Shutdown` carve-out question.

FACT: `lenny_adapter_leaked_slots` is a GATEWAY-emitted gauge despite §6.2:160 saying "The adapter exposes"; it is `gatewaymetrics.Metrics.SetAdapterLeakedSlots`, fed from `applySlotRetryPolicy`'s `leakGauge`. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-223, gatewaymetrics.go:1213-1217, pkg/gateway/sessionserver/start.go:2843-2846. So SPEC-3's claim that a pre-`running` leak "is surfaced on the `lenny_adapter_leaked_slots` gauge" is TRUE even though the adapter never learns of that leak. Do not file the "the adapter cannot see this leak" argument; the standing Traps entry is right and this is the code proof.

WATCHOUT: SPEC-3's §5.2 append states the leak predicate one-armed — "A cleanup on that path that does not complete is still accounted: the adapter's `Shutdown` response for that reclaim does not report a clean exit" — while staged §7.1 states it two-armed ("A reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit"). EVIDENCE: spec-changes.md:600 vs :392. I judged this the same family as the twice-refuted "§15.4 omits an obligation §4.7 carries": §7.1 is the normative home, the §5.2 sentence answers a narrower question (how a withheld-report cleanup is still accounted), and no implementor is left without an answer. A future filing here has to show a reader of §5.2 alone reaching a WRONG disposition, not merely an incomplete one.

WATCHOUT: the staged §7.1 parenthetical "(releases the pod claim, and reclaims the state the attempt created on the pod)" lands inside the atomicity paragraph whose own heading scopes it to steps 2–8, and spec/07_session-lifecycle.md:6-21 confirms those steps issue no pod-side RPC (the finalize block is steps 11–13). This is the "Vacuous parenthetical at steps 2-8" finding, already raised and retired (review-log.md:869, review-log-archive.md:357-359, :3107, :1812). Do not re-file; §29.2's mirror at spec/29:199-201 is vacuous for the same reason.

USEFUL [Standing context / Traps, "Dead end: `releases the slot reservation afterwards` contradicts the leaked hold"]: I reconstructed this independently from `ReleaseSlotReservation`'s hard-coded `leaked=false` (pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503) and the staged §7.1's unconditional "releases the slot reservation afterwards", and would have filed it. The Trap plus CODE-4's "`ReleaseSlotReservation` takes the disposition" (non-spec-changes.md:498) and its "Reading that as leaked would withhold the pod's slot-counter decrement" (non-spec-changes.md:594-596) kill it. Keep the entry.

USEFUL [review-log.md:810 and review-log-archive.md:18076]: the per-connection versus per-session epoch-latch granularity in §4.7.1 :683 is still filed as OPEN in the standing context and the text is unchanged, so it was evidently refuted on materiality; archive:18076's fact that `connectSlot` dials a fresh adapter connection per bind attempt is the reason. Having both saved me from re-filing it as predicate drift.


### [spec-recheck.4.review-applicability.1]

DECISION: Returned an empty findings list — BECAUSE every staged anchor in
`...spec-changes.md` resolves uniquely against the current tree, every created
artifact has the properties its referring edits need, and no existing gate I
could find hard-fails on the staged spec text. ALTERNATIVES: filing the
SPEC-4 `running`-boundary tension a third time, and filing the §7.2 step-2
mid-line anchor; both are already adjudicated non-findings (see below).

FACT: `spec-changes.md` has NOT changed since the `spec-recheck-r2` snapshot's
successor. `diff -q` of the working file against the `spec-recheck-r3`,
`spec-recheck-r3-start`, `spec-recheck-r4` and `spec-recheck-r4-start`
snapshots is empty; the last edit was between r2 and r3 and is two
reworded sentences only (the `running`-boundary gloss). Do not spend a round
diffing against `spec-recheck-r4`; diff against `spec-recheck-r2` to see the
last real delta. EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r2 vs
proposals/0081_.../0081_....spec-changes.md

FACT: the last spec delta closed the standing trap "SPEC-4's prose against the
untouched `receiving_uploads ──→ running` trigger" by taking the trap's second
option — deleting the "which is the moment the trigger above names" clause and
rewording the in-flight case from "the runtime has not yet acknowledged" to
"has not yet reached the runtime". With the fence annotation reading
"session dispatched to runtime with its session identifier" (spec/06:152-153),
a start the adapter admitted but has not yet dispatched is still
`receiving_uploads` under both texts, so the contradiction is gone rather than
relocated. EVIDENCE: spec/06_warm-pod-model.md:152-153;
0081_....spec-changes.md:87-96, :660.

FACT: I re-verified every staged anchor against the tree this round. All are
present and, with their stated scoping, unique: spec/04:157 (§4.1 third
sentence), spec/04:686 (`Shutdown` row opening), spec/04:854 (§4.7.9 step 5),
spec/04:659/688/695 (§4.7.1 insertion window), spec/05:453 (Scrub model),
spec/05:545 (Slot cleanup bullet), spec/05:555 (Max retries),
spec/06:152-153 (fence) and :158 (`reserved` hold semantics),
spec/07:23 (atomicity parenthetical), :210 (§7.2 preamble), :213 (step 2),
:214 (step 3), :414 (§7.3 list tail), spec/06:234 (mid-resume cancel bullet),
spec/15:1469/1471 (§15.4 insertion window), spec/29:711 (§29.4 step 13).

FACT: the tier-11 gate that reads the §4.7 `Shutdown` row survives SPEC-1
untouched. `TestRecycleScrubTriggerAgrees`-family requires the row to carry
"recycle disposition", "ReportPodScrub", the three recycle parameters,
"does not block the response on the scrub", and the
`05_runtime-registry-and-pool-model.md#52-...` link; the staged replacement
keeps the row tail from "On the default disposition the pod is replaced."
onward and re-adds the §5.2 link in its own new text, so all seven substrings
hold. `requireLine` returns the FIRST `| \`Shutdown\` |` line in `### 4.7 `,
and SPEC-5's §4.7.1 block is inserted after the tables, so the row stays
first. EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-100,:135-148.

FACT: SPEC-3's append to the `**Scrub model.**` paragraph (spec/05:453) does
not redirect any `lineContaining`-style §5.2 gate. The four live §5.2 anchors
are "Whole-pod replacement trigger", "Session count limit",
"**Fresh-guest reprovision:**", "**Slot (session mode).**",
"The gateway triggers the whole-pod scrub" and
"the pod is held for its tenant through the claim's `reserved` state"; none
appears in the appended text, and the append's own
"whole-pod replacement trigger stated below" is lowercase where the gate is
capitalised. There is NO tier-11 gate anchored on "Slot cleanup:" or on
"max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)", even though the
appended reclaim-hold paragraph quotes both — so the case-sensitivity margin
the standing trap names is real but not the only thing holding. EVIDENCE:
tests/tier11_docs/{concurrent_slot_lifecycle,recycle_scrub_trigger_consistency,vm_restart_reprovision_consistency,slot_definition_glossary}_doc*_test.go;
grep of tests/tier11_docs for "Slot cleanup" returns nothing.

WATCHOUT: the §7.1 pod-side-reclaim paragraph lands INSIDE §7.1's fenced code
block (the fence opens at spec/07:5 and closes at :54, and the atomicity
paragraph it follows is at :23). Markdown links in it will not render. That is
the shipped precedent — the atomicity paragraph already carries
`[§6.2](...)` inside the same fence — so it is not a finding, but an agent who
"discovers" it should not re-file it. EVIDENCE: spec/07_session-lifecycle.md:5,:23,:54.

USEFUL [Traps, review-log.md:483ff]: the trap block saved me from re-filing
three things — the §29.4 step-13 anchor's non-uniqueness (four sites, resolved
only by the "numbered step 13" scoping), the §7.2 step-2 mid-line append
(deterministic, lands before the `final_workspace_ref` sentence), and the
SPEC-4 `running`-boundary item. Read the Traps section before the staging.


### [spec-recheck.4.review-citations.1]

FACT: the spec-changes.md staging did NOT change between spec-recheck r3 and r4. `diff -rq` of
scratchpad/cp-snap/.../spec-recheck-r4 (and spec-recheck-r4-start) against the proposal directory is
empty, and the r3 snapshot differs only in the review log. So there was no delta to look at first;
the whole staging is r3-converged text. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r4

FACT: every concrete citation in spec-changes.md was re-verified against the tree this round and all
hold. Verbatim anchors: spec/04_system-components.md:157 (§4.1 third sentence), :686 (§4.7 `Shutdown`
row opening), :854 (§4.7.9 step 5); spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`),
:545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**`); spec/06_warm-pod-model.md:150-153
(fence heading + `receiving_uploads ──→ running`), :158 (`**\`reserved\` hold semantics.**`), :234
(`resuming → cancelled` clause); spec/07_session-lifecycle.md:23 (atomicity parenthetical), :24 (the
continuation line), :210 (§7.2 preamble premise), :213 (step 2 tail), :214 (step 3), :414 (§7.3 list
tail); spec/29_communication-scenarios.md step 13 tail. Numeric citations: proto:1630-1635
(`coordination_generation` on `ShutdownRequest`), spec/04:151, :157, spec/29:586-588, :589-591, :697,
:669-674 — all say what the proposal says they say.

FACT: the four attributed-behaviour claims in the commentary are true in the tree.
`slotlayout.RemoveTree` removes `p.CredentialsDir` (pkg/adapter/slotlayout/tree.go:59-69);
`deregisterSlotLocked` cancels every armed timer before deleting the entry
(pkg/adapter/slotsession.go:174-181); `stageWorkspace` sends `PrepareWorkspace` only under
`if len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323-1330); the claim path
returns `WARM_POOL_EXHAUSTED` with `concurrent_slots_exhausted`
(pkg/gateway/sessionserver/start.go:185). `DemoteSDK` does remove the entry, inside the RPC, via
`releaseSessionSlot` (pkg/adapter/sdkwarm.go:296-299), which is what makes §5.2's
"a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC
answers" true.

FACT: all seven bind-sequence RPCs §4.7.1 enumerates are rows in the §4.7 Gateway → Adapter table
(spec/04:659-687), and `PrepareWorkspace` is the only client-streaming one
(schemas/lenny-adapter.proto:41). `ShutdownRequest`/`ShutdownResponse` are named in exactly one spec
site, §4.1 at spec/04:157, which SPEC-1 edits, so the proto edit strands no other spec surface.
`Shutdown` appears nowhere in spec/28, so the "no §28 register row" claim is true by absence checked
in the tree rather than by register.

WATCHOUT: the gateway dials a FRESH adapter connection per bind attempt
(`b.DialAdapter` at pkg/gateway/podlifecycle/podsession/binder.go:1776 inside `connect`, and again at
:1152 in `ReclaimClaimed`, :1178 `ReadoptConnect`). That is what makes §4.7.1's connection-scoped
caller-latching rule safe: no two sessions share one connection, so "the most recent epoch a response
reported to it on one adapter connection" cannot latch another session's epoch. If a future change
introduces a per-pod pooled adapter connection, that caller rule becomes wrong and the fence silently
degrades to always-`superseded` (a leak, not a wrong teardown). Do not re-file it against the current
tree; do re-check it if connection reuse lands.

DECISION: returned zero findings BECAUSE the citation lens found nothing false, no attribution to the
wrong component, and no anchor that fails to resolve. ALTERNATIVES considered and rejected as below
the bar: (a) §5.2's widened action list still omits `/sessions/{sessionId}` and `/artifacts/{sessionId}`,
which `RemoveTree` also deletes (pkg/adapter/slotlayout/tree.go:60) — a pre-existing incompleteness the
edit neither introduces nor makes false; (b) §4.7.1's "the generation ... is validated on the RPCs that
carry it" is true of the spec (spec/07:216, proto:1631-1634) but not of the shipped adapter, which
validates it only in `CoordinatorFence` and `CheckpointBarrier` — a pre-existing spec/code divergence
this proposal neither states nor widens; (c) the §4.7 `Shutdown` row and the RPC tables physically sit
under the `#### 4.7.1` heading (spec/04:659) while the proposal labels them "§4.7", which is the
repository's own established cross-reference convention (spec/05:453 does the same).


### [spec-recheck.4.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens on the spec staging — BECAUSE every externally-consumed representation the staged spec edits touch has a home, and the delta this round is three sentences of wording. ALTERNATIVES: I considered filing the tier-10 CONF-1 gap (below) and the docs/reference/adapter-contract.md `ReportSessionScrub` row; both fail this loop's scope (their fix lands in the non-spec staging) and the second is not even wrong after SPEC-3.

FACT: the whole delta since this lane's last converged review is three sentences, all in the same rewrite. `diff -u scratchpad/cp-snap/.../spec-recheck-r3/...spec-changes.md proposals/.../spec-changes.md` is EMPTY; the last content change to the file is commit 645bd1d67 and it is visible only against the `spec-recheck-r2` snapshot. It replaces "the runtime's acknowledgement of it" with "the moment the session reaches the runtime" (Design preamble and the §4.7 `Shutdown` row) and drops the "which is the moment §6.2's `receiving_uploads → running` trigger names" clause from the two `running`-boundary statements, replacing it with "given the session with its session identifier". EVIDENCE: proposals/0081_.../0081_....spec-changes.md:21, :322, :90-93, :661.

FACT: the client-surface absence sweep for the bind epoch comes back empty on every parallel representation outside the proto. No SDK under `sdks/` references `StartSessionResponse`, `AssignCredentialsResponse` or `ShutdownRequest` (the adapter proto is gateway↔adapter only; the runtime SDKs speak JSONL), `pkg/embedded` carries no copy, and §28 has no per-message register row for `Shutdown` (grep of spec/28 for `Shutdown` returns only the CH-RUNTIMEOPS degradation row at :1819). EVIDENCE: schemas/lenny-adapter.proto:41-206; spec/28_communication-channels.md:1819.

FACT: `examples/runtimes/echo/`, which spec/15:1470 names as the executable reference adapter built from the same `.proto`, DOES NOT EXIST in this tree. The echo artifacts are runtimes (`cmd/runtimes/echo`, `pkg/runtimekit/echocore`, `sdks/runtime/*/example*/echo`), which speak JSONL and implement no adapter RPC. So a proto addition on the gateway↔adapter surface has no reference-adapter mirror to update. This is a pre-existing spec/tree mismatch, not 0081's. EVIDENCE: spec/15_external-api-surface.md:1470; `find . -type d -name "echo*"`.

FACT: `ABORTED` was already the adapter's busy-retry status before this proposal, so §15.4's new "refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on" overloads nothing. `errOpBusy`/`errOpCoalesced` surface as `codes.Aborted` "the same status a busy lock returns, so the gateway ... retries with backoff". EVIDENCE: pkg/adapter/oplock.go:29-39; pkg/adapter/checkpoint.go:115. Also: `ABORTED` appears nowhere in spec/ before this edit (grep spec/*.md), and `UNIMPLEMENTED` at spec/15:1470 is the precedent for naming a bare gRPC code in §15.4.

FACT: spec/15:1462 claims `schemas/lenny-adapter.proto` "Includes the structured error code enum with categories (transient, permanent, policy)". The proto declares only three enums and none of them is that: `SessionScrubOutcome` (:438), `PodScrubOutcome` (:478), `CheckpointTrigger` (:1159). Pre-existing and outside 0081's blast radius, but it is why §15.4 can publish a bare gRPC status without contradicting a published error-code contract — there is no such contract in the artifact.

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's caller rule ("`DemoteSDK` ... removes the entry") is grounded: the handler calls `s.releaseSessionSlot(s.anyRegisteredSession())`. EVIDENCE: pkg/adapter/sdkwarm.go:291-299.

DEFERRED [0081_....non-spec-changes.md]: CONF-1 states "Four properties, one per part of the §15.4 contract" (non-spec-changes.md:~1095) but §15.4 now publishes TWO fenced blocks, and all four properties are bind-epoch properties. Nothing in the tier-10 battery asserts the reclaim-hold block's own published non-conformance cases (admitting a create-or-resolve request while the cleanup runs; refusing one with a permanent status). What is true instead: the hold's wire behaviour is pinned only below tier 10. I did not file it: the remedy is a test deliverable and this loop may not edit that file. A later non-spec pass should decide whether CONF-1 grows a fifth property or the "one per part" sentence is rescoped.

UNVERIFIED: whether the retained `running`-boundary prose ("a start still in flight, whose session has not yet reached the runtime, leave the slot in `receiving_uploads`") still sits comfortably against the UNEDITED §6.2 trigger annotation "workspace ready, session dispatched to runtime with its session identifier" — the fix removed the explicit citation of that trigger rather than reconciling the two phrasings. Three earlier rounds fought over this exact pair, so I did not re-open it; a mechanism lens should decide once whether "dispatched to" and "has reached" are the same instant. EVIDENCE: spec/06_warm-pod-model.md fence entry `receiving_uploads ──→ running`; proposals/0081_....spec-changes.md:90-93, :661.


### [spec-recheck.4.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE `0081_….spec-changes.md` is byte-identical
to BOTH the `spec-recheck-r3` and the `spec-recheck-r4` snapshots (`diff -rq` against
`scratchpad/cp-snap/…/spec-recheck-r4` is silent; against `spec-recheck-r3` only the review log
differs), so this lane's staged spec text has now not changed for three consecutive rounds and my
lens has already walked it twice to empty — ALTERNATIVES: re-filing the shared-entry teardown /
residue-class-one / unfenced-first-RPC rows as "accepted failure mode with no landing spec
sentence"; rejected for the reasons `spec-recheck.1.review-docs-alignment.1` and
`spec-recheck.2.review-docs-alignment.1` already recorded, which I re-verified rather than
re-derived.

FACT: the orchestrator brief for this round instructs a diff against
`scratchpad/cp-snap/…/spec-recheck-r4`, and that diff is EMPTY. The recheck loop can fire with a
nil delta. Confirm the delta exists before budgeting a round on "read what changed first".
EVIDENCE: `diff -rq scratchpad/cp-snap/0081_…/spec-recheck-r4 proposals/0081_…` returns nothing.

USEFUL [spec-recheck.2.review-docs-alignment.1]: its row-by-row map of the eleven
`## Edge cases and accepted failure modes` rows onto landing staged spec sentences
(review-log.md:1116) is complete and still accurate against the current file; I spot-checked the
five "resolves" rows (:322, :390, :600, :713, :653, :352) and the four "no landing sentence" rows
(:183-188, :214-231, :233-249, :250-261) and found no drift. A future docs-alignment pass on this
lane can start from that map instead of re-walking.

USEFUL [spec-recheck.1.review-docs-alignment.1]: its WATCHOUT that this lens is nearly inert on a
spec-only loop (the only in-scope shapes are an accepted failure mode with no landing spec
sentence, and a spec-internal inconsistency) held for a third round.

OPEN: three residues carried by the amendment — the shared-entry teardown of a serving session
(spec-changes.md:183-188, :233-249), residue class one via the start/reclaim race (:216-232) and
via the late `StartSession` (:250-261) — still have their observable outcome stated only in
proposal reasoning, in no staged spec sentence and in no `docs/` page. Three lens passes have
declined to file it as documentation completeness. If a human wants that outcome published, the
decision has to be taken outside this loop; it will not surface from this lens again.


### [spec-recheck.4.review-edit-sites.1]

FACT: this round's spec staging is BYTE-IDENTICAL to the previous round's. `diff -q -r scratchpad/cp-snap/.../spec-recheck-r4 proposals/0081_...` returns nothing at all, and against `spec-recheck-r3` the only differing file is the review log (the compaction pass). So spec-changes.md has not moved since spec-recheck-r2. A "recheck" round whose named delta is empty should be spent on breadth, not on a diff. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r4 vs proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry

DECISION: returned an EMPTY findings list for the edit-site-completeness lens — BECAUSE every spec-side surface the staged identifiers touch is either edited, correctly scoped out with a stated reason, or a pre-existing divergence the standing Traps already bar. ALTERNATIVES rejected, each with what killed it: (a) `§4.7`'s `ReportSessionScrub` row ("Report the outcome of the per-slot cleanup at a session release", spec/04_system-components.md:692) going stale under SPEC-3's withheld report — barred by the standing "is a pre-start reclaim a session release?" dead end (review-log-archive.md:654), and the staged §5.2 text keeps the pre-`running` cleanup outside the term "session release" ("It ALSO runs when a bind is abandoned...", spec-changes.md:601); (b) §6.2's `slot_cleanup ──→ leaked` gloss "(cleanup timeout exceeded)" (spec/06_warm-pod-model.md:148) not covering the new "reclaim did not complete" producer — barred by the standing Trap at review-log.md:528, pre-existing against spec/05:545; (c) the §7.2 preamble premise SPEC-2 deletes ("no live workspace on the pod to seal") surviving elsewhere — grepped `live workspace` / `no runtime was started` / `not yet reached \`attached\`` across spec/ and docs/: the only other site is §7.2's own "Pre-attach terminal collapse" block (spec/07_session-lifecycle.md:220), which is scoped to `resume_pending` where no pod is claimed at all and stays true; (d) §28.5.3's `terminate` frame row (spec/28_communication-channels.md:1082) needing the co-tenant gating SPEC-1/§29.4 add — that row states the frame's schema and the runtime's obligation, never when the adapter emits it, so it is not an emission-condition site; (e) §15.4.2's `DRAINING` prose and §15.4.3's level table — neither states a per-session-end emission rule.

FACT: the two remaining §4.7-adjacent claims I re-derived from scratch both hold. `DemoteSDK` really does remove the registry entry (`s.releaseSessionSlot(sessionID)` after `anyRegisteredSession`), so §4.7.1's caller rule "a caller holds none once it has itself issued an RPC that removes the entry" names a real RPC. And §15.4.6's conformance suite really does run the runtime against a FAKE adapter, so "no adapter under test" is accurate and CONF-1 belongs in tier 10 rather than there. EVIDENCE: pkg/adapter/sdkwarm.go:295-298; spec/15_external-api-surface.md:2038-2046

FACT: the seven bind-sequence RPCs §4.7.1 enumerates really are the complete set of entry-creating/entry-resolving handlers. Entry creation funnels through `ensureSlotStateLocked`, whose only production reachers are `ensureSlotPaths` (called from `resolvePrepareStagingDir`, `FinalizeWorkspace`, `RunSetup`), `assignCredentialsSlot` (AssignCredentials) and `claimSessionSlotUnderLock` (StartSession, Resume, SDK-warm ConfigureWorkspace). No eighth handler touches the registry. EVIDENCE: pkg/adapter/slot.go:105,:140; pkg/adapter/staging.go:134,:181,:337; pkg/adapter/slotcreds.go:26; pkg/adapter/slotsession.go:75; pkg/adapter/session.go:111; pkg/adapter/resume.go:50; pkg/adapter/sdkwarm.go:217

USEFUL [review-log.md:528; review-log-archive.md:654]: the two standing dead ends (the `leaked` gloss and the "session release" defence) each killed a candidate finding within minutes. They are the highest-value entries in the standing context for this lens.

WATCHOUT: the scope note for this loop reduces the edit-site lens almost to spec/ alone. `schemas/` is SCHEMA-1's and `docs/` is DOCS-1/DOCS-2's, both in the non-spec lane, so a docs or proto edit-site gap costs two verifiers and closes nothing here. `charts/` carries no identifier this proposal touches (no new tunable is minted; the hold reuses `cleanupTimeoutSeconds`), and the only `slot_cleanup` hit outside spec/ is a proto comment at schemas/lenny-adapter.proto:442. EVIDENCE: schemas/lenny-adapter.proto:442


### [spec-recheck.4.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens — BECAUSE every actor the staged spec assigns an action can perform it, verified against the tree rather than against the register — ALTERNATIVES: I considered filing the per-connection epoch latch as wrong-grained on a concurrent pod (two sessions sharing one pod connection would mix epochs), and dropped it: the gateway dials one adapter connection per bind attempt and retains it for that session (non-spec-changes.md:1060-1066, `BindResult.Adapter`), so per-connection is per-attempt.

FACT: the snapshot the orchestrator names for this round is byte-identical to the working tree, so `diff -ru .../spec-recheck-r4 proposals/0081_...` is empty. The last real delta to the spec staging is `spec-recheck-r2 → HEAD`, two reworded sentences only: "at the runtime's acknowledgement of it" → "at the moment the session reaches the runtime" (spec-changes.md:21, :324) and the `running` boundary dropping its citation of the `receiving_uploads → running` trigger while keeping that trigger's tail ("with its session identifier", spec-changes.md:90, :661). EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r4 vs proposals/0081_...

FACT: all seven RPCs §4.7.1 names as the bind sequence exist and every one of them resolves a registry entry through `ensureSlotPaths`/`ensureSlotStateLocked`, so "each reports the entry's current epoch on its response" is implementable at every one of them, `RunSetup` included. EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151; pkg/adapter/staging.go:337 (RunSetup), :181 (FinalizeWorkspace), :134 (PrepareWorkspace staging), pkg/adapter/slotcreds.go:26 (AssignCredentials).

FACT: `DemoteSDK` really does remove the registry entry, so §4.7.1's "it holds none once it has itself issued an RPC that removes the entry the epoch names: `DemoteSDK` ..." is grounded. EVIDENCE: pkg/adapter/sdkwarm.go:295-297 (`anyRegisteredSession` then `releaseSessionSlot`).

FACT: §5.2's "for a hold taken outside any request, by the termination window of the pass that runs the cleanup" has a real referent — the coordinator-lost hold sweep tears a slot down with no RPC in scope. EVIDENCE: pkg/adapter/holdstate.go:248-254.

FACT: the §5.2 `**Max retries:**` exclusion is evaluable at placement time only because the compensation is synchronous inside the bind wrapper: `sbe.Leaked = b.compensateFailedSlotBind(...)` runs before the retry policy re-enters. A future reviewer tempted to file "the gateway cannot know whether the reclaim completed when it places the retry" should read that line first. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:640.

FACT: `Shutdown` appears in no §28 register row (grep over spec/28 returns nothing), and `exited_cleanly` appears nowhere in spec/ or docs/, so the two "deliberately untouched" claims that rest on those absences hold today.

WATCHOUT: spec/15_external-api-surface.md:672 says the fine session state `receiving_uploads` is "tracked solely in the Postgres session model", while §6.2 carries it as a per-slot sub-state the gateway tracks in `pkg/sandbox/slotstate`. That tension is PRE-EXISTING and the new `receiving_uploads → slot_cleanup` edge adds nothing to it (§15 enumerates no slot transitions and never named `slot_cleanup`). Do not file it against this proposal.


### [spec-recheck.4.review-fresh.1]

FACT: the spec-changes.md staging did NOT change between spec-recheck r3 and r4. `diff -u scratchpad/cp-snap/.../spec-recheck-r3/...spec-changes.md` and `.../spec-recheck-r4-start/...` against the working tree both return empty; the only delta under spec-r4 is in the implementation-checklist and non-spec-changes files, which this loop may not file on. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md (761 lines, unchanged)
DECISION: returned an empty findings list — BECAUSE every anchor, citation and mechanism I could reach verified clean — ALTERNATIVES: I considered and dropped four candidates, each recorded below so the next agent does not re-spend the effort.

FACT: a mechanical anchor sweep over every fenced block in spec-changes.md is cheap and currently clean. Script: extract every ```...``` block, count occurrences across spec/*.md. Every "text to replace" block occurs exactly once repo-wide; every "replace it with" block occurs zero times. Re-run it rather than re-deriving by hand. EVIDENCE: spec/04_system-components.md:157, :686, :854; spec/05:453, :545, :555; spec/06:150-153, :234; spec/07:23, :210-214, :414; spec/15:1469; spec/29:711
WATCHOUT: the §29.4 step-13 anchor string `([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).` occurs FOUR times in spec/29 (:645, :711, :887, :982), two of them inside §29.4 itself. It resolves only through the "In §29.4's numbered step 13" scoping. Do not file it (already recorded as accepted in the standing context) and do not "fix" it by widening the quote without checking :645.

FACT (dropped candidate 1). §5.2's widened `**Slot cleanup:**` action list omits `/sessions/{sessionId}` and `/artifacts/{sessionId}`, which `slotlayout.RemoveTree` also removes. NOT a finding: §6.4 owns those two trees and already states the adapter "removes it during slot cleanup". EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; spec/06_warm-pod-model.md:386
FACT (dropped candidate 2). §5.2's appended "A cleanup on that path that does not complete is still accounted: the adapter's `Shutdown` response for that reclaim does not report a clean exit" is NOT falsified by the shipped `ExitedCleanly: closeErr == nil`, because CODE-2/CODE-1 restage it as `closeErr == nil && (live || treeErr == nil)`. On the pre-`running` path `live` is false, so the tree-removal error does surface. EVIDENCE: 0081_...non-spec-changes.md:252
FACT (dropped candidate 3). §4.7.1's caller rule is per-CONNECTION, and that is sound only because the gateway dials a fresh adapter client per bind attempt. It does: `connectSlot` calls `b.DialAdapter(addr)` inside each bind. A shared per-pod connection would break the latch on a concurrent pod. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:415-462
FACT (dropped candidate 4). §4.7.1's "`DemoteSDK` … removes the entry" is true in the tree: the handler calls `s.releaseSessionSlot(sessionID)` after `anyRegisteredSession()`, and that deregisters. The `([Section 4.7](#47-runtime-adapter))` citation attaches to the pod-warm-state half of the sentence, not to the entry-removal half, so it is not a false citation even though §4.7's `DemoteSDK` row says nothing about the registry. EVIDENCE: pkg/adapter/sdkwarm.go:296-299; pkg/adapter/slotsession.go:214-220; spec/04_system-components.md:674

FACT: the `RunSetup` handler does resolve a registry entry (`s.ensureSlotPaths(sessionID)`), so §4.7.1's "each of the seven bind-sequence RPCs reports the entry's current epoch" is implementable for all seven. `PrepareWorkspace` is the only streaming one and §4.7.1 carves it out explicitly. EVIDENCE: pkg/adapter/staging.go:337; schemas/lenny-adapter.proto:41
FACT: `Shutdown` appears in ZERO rows of spec/28, so the "§28's registers deliberately untouched" claim verifies by `grep -c Shutdown spec/28_communication-channels.md` = 0.
FACT: the `terminate` frame really does carry only `type`, `deadlineMs`, `reason` and names no session, so the §4.7/§29.4 "pod-global and names no session" clause is accurate rather than assumed. EVIDENCE: spec/28_communication-channels.md:1082
WATCHOUT: §10.1 already uses the bare noun "hold state" for the coordinator hold, and it rejects all other inbound RPCs with `UNAVAILABLE`, while this proposal's reclaim hold refuses with `ABORTED`. Two different holds, two different codes, no stated precedence when both apply. I judged this below the bar (no conforming implementation acts differently), but a future reviewer who wants to file it must argue a harm, not a naming collision. EVIDENCE: spec/10_gateway-internals.md:57


### [spec-recheck.4.review-kubernetes.1]

FACT: The r4 snapshot is byte-identical to the live proposal directory, and so is spec-recheck-r3.
`diff -rq scratchpad/cp-snap/.../spec-recheck-r4/ proposals/0081_.../` and the same against
`spec-recheck-r3` both return nothing. There was no delta to read first this round; the whole
staging is the scope. A future round should run that diff before planning a "read the delta"
pass, because the orchestrator prompt promises a delta that may not exist.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r4

DECISION: Returned an empty findings list under the Kubernetes-idiom lens. BECAUSE the staged
spec edits touch no CRD status subresource, no field manager, no finalizer, no admission
webhook predicate, and no controller reconcile. Every mechanism the amendment adds is
process-local or on the synchronous gateway→adapter gRPC surface: the bind epoch is explicitly
"pod-local, and never persisted" (spec-changes.md:678 block), and the reclaim hold is adapter
in-memory state. ALTERNATIVES: I checked and rejected five candidate angles, listed below so the
next Kubernetes-lens agent does not re-derive them.

FACT: The staged spec text writes nothing to any CRD. The only CRD-adjacent acts it names are
"releases the pod claim" and "the half-claimed replacement pod is released to the pool", both of
which are `SandboxClaim.status` binding-state patches the gateway already owns per §4.6.3.
The gateway writes no `Sandbox.status`; the WarmPoolController is the sole writer, and the
occupancy projection is level-triggered off claim watches.
EVIDENCE: spec/04_system-components.md:409; spec/07_session-lifecycle.md:225 ("the gateway writes
no `Sandbox.status` field, and the WarmPoolController is the sole writer").

FACT: §6.2's per-slot sub-states are NOT on any CRD. "There is no per-slot phase value:
concurrent occupancy projects `claimed`". So SPEC-4's new `receiving_uploads → slot_cleanup`
edge adds nothing to etcd and cannot race a field manager. Do not file it as a status-write.
EVIDENCE: spec/04_system-components.md:409; spec/06_warm-pod-model.md:150-156.

WATCHOUT: `spec/06_warm-pod-model.md:283` ("**Pre-attached failure retry policy:** ... The pod is
marked `failed` and released back to the pool (or terminated if unhealthy)") reads as if the pod
is REUSED, which looks like it falsifies the staged §7.1 sentence "the pod retires under the
§6.2 pre-attached failure disposition, so the reclaim's residue does not outlive the pod". It is
not a live finding: the review-log archive already adjudicated this family, and on an exclusive
recycling pool the residue is collected by the occupancy-zero whole-pod scrub. Argue from a harm
the whole-pod scrub does not kill before re-filing it.
EVIDENCE: spec/06_warm-pod-model.md:283; refuted-list entry "Staged §7.1 obliges a pod-side
reclaim on the creation finalize block".

FACT: `lenny_adapter_leaked_slots` is described in the spec as an adapter gauge but is actually
registered and published by the gateway (`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:223`).
That mismatch is pre-existing and predates this proposal; SPEC-3's one mention of the gauge does
not make it worse.
EVIDENCE: spec/06_warm-pod-model.md:160; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223.

UNVERIFIED: SPEC-2's staged §7.2 step 3 has the gateway send the reclaim "on the connection that
attempt still holds" during a mid-resume cancel, while step 1 of the same shipped sequence
cancels the in-flight restoration RPCs and step 4 bumps `coordination_generation`. Whether the
replica handling `DELETE /v1/sessions/{id}` is always the replica holding that adapter connection
(and therefore the per-connection epoch §4.7.1 latches) is not stated anywhere I could find. I
did not file it because it is a distributed-coordination question rather than a Kubernetes-idiom
one and the mid-resume carve-out was already resolved as open decision 15. A concurrency or
material lens should settle it.
EVIDENCE: spec/07_session-lifecycle.md:212 (step 1), :216 (step 4); spec-changes.md §7.2 step 3
replacement block.


### [spec-recheck.4.review-mechanism.1]

DECISION: returned an EMPTY findings list for the end-to-end-mechanism lens on the r4 spec staging — BECAUSE the whole delta since the last mechanism pass is three wording swaps that close the boundary contradiction they were written for, and every fresh angle I derived independently collapsed into a family the log has already adjudicated. ALTERNATIVES rejected are listed below so nobody re-walks them.

FACT: the r4 delta is what the r3 shard predicted plus the r3 fixer's own edit, and the two provided snapshots are misleading. `diff -ru scratchpad/cp-snap/.../spec-recheck-r4 proposals/0081_...` is EMPTY (that snapshot is the working tree), and `diff -ru .../spec-recheck-r3 ...` shows only the review log. The staging delta is visible only against `spec-recheck-r2`: three hunks, all in spec-changes.md — Design :20-22 and the SPEC-1 §4.7 row :322 swap "rather than at the runtime's acknowledgement of it" for "rather than at the moment the session reaches the runtime", and Design :90-93 plus SPEC-4's §6.2 prose :660 drop the `receiving_uploads → running` citation and restate the boundary as "the pod's shared runtime process has been given the session with its session identifier" / "a start still in flight, whose session has not yet reached the runtime". Diff against spec-recheck-r2 if you want the real delta.

FACT: the reworded boundary is coherent across all four sites and against the code predicates. "the session reaches the runtime" == "the shared runtime process has been given the session" == `runtimeLive` (set only by `noteRuntimeStarted`, pkg/adapter/runtimegeneration.go:36-49), and the teardown's earlier moment is `st.started` (pkg/adapter/slotsession.go:88). Design :20-23, §4.7 row :322, the SPEC-4 fence annotation :643-645 and SPEC-4 prose :660 all read on that one pair. The paragraph no longer claims equality with the untouched trigger "(workspace ready, session dispatched to runtime with its session identifier)" (spec/06_warm-pod-model.md:152-153), and it defines "a start still in flight" as pre-boundary in its own sentence, so neither of the two boundary findings this loop already fixed survives in a new dress.

FACT: `DemoteSDK` really does both things §4.7.1's caller rule and §5.2's hold paragraph attribute to it — it removes the registry entry and runs the release synchronously inside the RPC, before it answers. EVIDENCE: pkg/adapter/sdkwarm.go:293-297 (`s.noteRuntimeClosed(sessionID); s.releaseSessionSlot(sessionID)` inside the handler, guarded by `anyRegisteredSession`). So "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it" (spec-changes.md:601) is verified against the tree, and so is "`DemoteSDK` ... removes the entry" (:682).

FACT: the standing-context claim that three refusals travel out of `ensureSlotStateLocked` (review-log.md:362) is STALE against the current non-spec staging, which gives it exactly one — the hold refusal, plus the mint on the creating branch, taking no epoch from the caller and comparing nothing (non-spec-changes.md:1010-1021). That matters to this lens because it is what makes the staged spec true: §4.7.1's "No epoch precondition applies to any request other than `Shutdown`" and the edge-case bullet's "its workspace, setup and credential RPCs resolve that entry ... whether or not its session has started, because the adapter refuses nothing on an epoch" (spec-changes.md:176-179) both hold against the staged code. I nearly filed a spec/code contradiction off the stale entry.

MISTAKE (nearly filed, four dresses, all killed by the log or by the tree):
(1) "§7.1 obliges the gateway to release the slot reservation after a `superseded` answer, over-releasing a reservation a live successor holds." Dies on the reservation being a per-pod Redis counter decrement that rolls back this attempt's own increment (pkg/gateway/podclaim/slotclaimer.go:838-885 `ReleaseSlot`; pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224 `BindReservedSlot` releasing "the create-time reservation increment"), and on the residual create-time-reserved double-release already being a recorded, unfixed accounting residue.
(2) "A `Shutdown` carrying both an epoch and the recycle disposition leaves the whole-pod scrub unfenced between §4.7's 'precondition on the whole request' and §4.1's unconditioned scrub clause." Already recorded unreachable at review-log-archive.md:16311 (the epoch is restricted to a compensation; the recycle disposition rides only the occupancy-zero `ShutdownRecycle`).
(3) "§6.2's `slot_cleanup ──→ leaked` gloss ('cleanup timeout exceeded', spec/06_warm-pod-model.md:148) does not cover the staged 'reclaim did not complete' producer." Barred by the standing Trap at review-log.md:528 and refuted at least four more times (archive:2914, :15413, :16322, :19992): the gloss is already narrower than spec/05:545 before this proposal applies.
(4) "The per-connection epoch latch is a granularity mismatch against §4.7.1's 'the epoch the caller holds for that session'." Already open-and-declined on materiality; `connectSlot` dials a fresh adapter connection per bind attempt (archive:18076), so connection and session coincide.

USEFUL [spec-recheck.3.review-mechanism.1]: its FACT that the §5.2 hold predicate's resolve arm is VACUOUS for every RPC (the hold starts after the entry is deregistered, so nothing can resolve one) is the single fastest kill for the whole "the hold refuses X" family, and it saved me from re-deriving the `Shutdown` carve-out question a sixth time.

USEFUL [Standing context, "'The pod's shared runtime process has been given' is bound vocabulary"]: this is what makes the r4 reword checkable at all. Without it the swapped phrase reads as new prose rather than as a pointer at `runtimeLive`/`runtimeCohort` membership.

WATCHOUT: the orchestrator's snapshot pointers for this lane are stale one round behind the staging. Always confirm with `diff -rq` against several `scratchpad/cp-snap/.../spec-*` directories before concluding "nothing changed"; the r3 shard concluded the staging was unchanged and the r3 fixer then edited it.


### [spec-recheck.4.review-operational.1]

DECISION: empty findings list for the operational-consistency lens on the spec staging — third consecutive empty return for this lens — BECAUSE `spec-changes.md` is BYTE-IDENTICAL to the `spec-recheck-r3` AND `spec-recheck-r4` snapshots (`diff -u` against both returns nothing), so no text in this lane's scope is new since this lens last swept it, and an independent re-derivation of every observability surface the staging touches reproduced only candidates the record already disposes of. ALTERNATIVES rejected, each with what killed it: (a) "§5.2:561's `**Whole-pod replacement trigger:**` bullet glosses `leaked` as `(cleanup timeout exceeded)` and §6.2:148's edge annotation does the same, while SPEC-3 routes a new leak producer (a reclaim answered without a clean exit) into that trigger and SPEC-2 declares the bullet stands as written" — standing Trap at review-log.md:528 and archive:2914; the gloss is already narrower than shipped `spec/05:545` ("If cleanup fails, the slot is leaked") and than shipped code (`leaked = err != nil || !cleanly`), so it is pre-existing; (b) "the SPEC-3 append asserts `leaked`/occupancy/trigger inside a paragraph declared concurrency-independent" — now split by pod concurrency in the current text and recorded refuted at archive:4027; (c) "§16 has no metric or alert for the hold refusal" — SPEC-3 states outright that "no report and no counter names it" and the gateway side accounts it as an ordinary slot failure, so the inventories agree; (d) "`ABORTED` appears nowhere else in `spec/`" — true (`grep -rn "ABORTED" spec/*.md` is empty) but §15.4 defines it inline, matching the bare-`UNIMPLEMENTED` precedent at spec/15:1469, and it mints no client-visible code.

FACT: there is no alert-catalog surface for any metric this staging names. `pkg/alerting/rules/` contains no rule mentioning scrub, leaked slots, or slot failure (`grep -rln "scrub\|leaked_slot\|slot_failure" pkg/alerting/rules docs/runbooks` returns nothing), and `spec/16_observability.md` names slot metrics only at :14 (`lenny_slot_failure_total`) and :15 (`lenny_slot_pod_replacement_total`), neither with an alert row. So no alert-to-runbook obligation and no tier-11 alert gate is reachable from these edits. EVIDENCE: spec/16_observability.md:14-15; pkg/alerting/rules/rules.go.

USEFUL [review-log.md:528, :1283, archive:4027]: the three standing entries on the `leaked` gloss, the prior empty return's rejected-alternative list, and the "on a pod of either concurrency" refutation between them killed every candidate I built, at the cost of one grep each rather than a filing.


### [spec-recheck.4.review-performance.1]

FACT: the spec staging did NOT change between spec-recheck-r3 and spec-recheck-r4. `diff -q` of
`0081_....spec-changes.md` against `scratchpad/cp-snap/.../spec-recheck-r3/` is SAME; the only
r2→now delta is two wording swaps ("the runtime's acknowledgement of it" → "the moment the session
reaches the runtime") at spec-changes.md:21, :91-94, :322 and :660. The orchestrator's
spec-recheck-r4 snapshot is byte-identical to the working tree, so `diff -ru` against it prints
nothing; do not read that as "no delta exists", read it as "the delta is against r3 or earlier".
EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r3/0081_....spec-changes.md

FACT: the durable-vs-replica-local leak-ledger split is PRE-EXISTING and SPEC-3's withheld report
does not widen it. `ScrubReporter.RecordSessionScrub` feeds the durable cross-replica
`ledger.RecordLeak` (which stamps `lenny.dev/drain-request`) only when the adapter reports
`leaked`, and the adapter's shipped `reportSessionScrub` derives that outcome from `closeErr`
alone — which is nil for a pre-`running` slot that never started a runtime. So today a pre-running
failed bind already reports `released`, never `leaked`, and the only leak signal on that path is
already the replica-local `slothealth.Tracker`. Withholding the report therefore removes no durable
signal. I nearly filed this as a top-tier reliability regression; it is not one.
EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481;
pkg/adapter/session.go:262-279; pkg/adapter/sessionscrubreporter.go:61-84;
pkg/gateway/runtime/slothealth/slothealth.go:56-67

FACT: SPEC-3's "a cleanup on that path that does not complete is still accounted: the adapter's
`Shutdown` response for that reclaim does not report a clean exit" is reachable ONLY because the
code lane changes `ExitedCleanly`. The shipped handler sets `ExitedCleanly: closeErr == nil` and
discards the tree-removal error (`_ = removeSlotTree(st)`), so on a pre-`running` slot — where no
runtime close runs at all — the response would always be clean and the accounting clause could
never fire. CODE-1 stages `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, which is
what makes the staged spec sentence true. If that code arm is ever dropped, the SPEC-3 clause
becomes an unreachable trigger.
EVIDENCE: pkg/adapter/session.go:270-272,:291; non-spec-changes.md:223,:252,:1443-1445

FACT: the gateway dials a FRESH adapter `*adapterclient.Client` per bind attempt
(`b.DialAdapter(addr)` inside `connect`/`reconnect`/`slotbinder`), so §4.7.1's per-connection
epoch latch is coherent. It is not a shared per-pod multiplexed ClientConn, which would have made
"the most recent epoch a response reported to it on one adapter connection" mix sessions. I
checked this specifically because that reading would have been a fatal top-tier defect.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:57-59,:1152,:1776;
pkg/gateway/podlifecycle/podsession/slotbinder.go:237,:459

UNVERIFIED: SPEC-3 states "a bind refused this way is accounted by the gateway as an ordinary
transient slot failure." On the retry path that means `health.RecordFailure`, and at
`maxConcurrentSessions: 2` the threshold is `(2+1)/2 = 1`, so ONE such refusal drains the whole
pod. I did not file it because none of the three requests the proposal says can meet the hold
(a §7.4 mid-session upload, a client-driven §7.3 resume, a create-time-reserved §15.1 start retry)
plainly traverses `applySlotRetryPolicy` — `BindReservedSlot` is called directly and never enters
it. Someone on the non-spec lane should confirm which code path actually records a hold refusal,
because if it does reach `health.RecordFailure` the hold turns a benign millisecond self-healing
refusal into whole-pod churn at concurrency 2.
EVIDENCE: pkg/gateway/sessionserver/start.go:2807,:2854-2858;
pkg/gateway/runtime/slothealth/slothealth.go:214-220; spec/05_runtime-registry-and-pool-model.md:561

DECISION: returned an empty findings list — BECAUSE the staging creates no new etcd status write,
no new CRD field, no new watch or informer, no Postgres or Redis write, and no new hot key: the
epoch is an in-memory pod-local `int64` per registry entry, the hold is a pod-local set keyed on
an identifier that equals the session identifier (so it can never block another session), and the
one net-new RPC is a single `Shutdown` per FAILED bind on a connection the attempt already holds.
Postgres failover, a Redis reset and a coordinator handoff all degrade to the shipped unconditional
teardown, which §4.7.1's "a caller that holds none sends the epochless form" states explicitly, so
nothing is less reliable than today — ALTERNATIVES: I considered filing the leak-ledger durability
split, the unreachable `exited_cleanly` accounting, the multiplexed-connection epoch mixing, and
the hold-refusal pod-churn amplification; the first three are refuted by the evidence above and the
fourth is recorded as UNVERIFIED for the non-spec lane rather than filed on a path I could not
confirm.


### [spec-recheck.4.review-reliability.1]

DECISION: returned an empty findings list for the reliability lens on the spec staging — BECAUSE the round-4 delta is two wording changes only (the `running` boundary phrase "the runtime's acknowledgement of it" → "the moment the session reaches the runtime" at spec-changes.md:21 and :322, and the removal of the §6.2 `receiving_uploads → running` trigger citation from SPEC-4's staged paragraph at spec-changes.md:661), and every recovery/restart/failover path I traced is either sound or already recorded as an accepted residue — ALTERNATIVES: I worked up and then killed four candidate findings, listed below, so the next reliability agent does not re-derive them.

FACT: agent pods run `RestartPolicy: Never`, so the adapter process never restarts in place inside a live pod. EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:980 (`RestartPolicy: corev1.RestartPolicyNever`), pinned at pkg/controller/sandbox/podspec/podspec_test.go:94-95. This is what makes the bind epoch's "unique and strictly increasing within one adapter process, pod-local, never persisted" claim (spec-changes.md:679) safe. The hazard I chased was: the gateway's per-pod handle is a `*grpc.ClientConn` (pkg/gateway/runtime/adapterclient/client.go:28-38,:48-54), which transparently re-establishes its transport, so "the connection the attempt already holds" is NOT a guarantee of one adapter process. Had the adapter container restarted in place, a caller could name an epoch minted by the dead process against a fresh process whose counter restarted low, and collide onto a successor's entry — a false `reclaimed` tearing down a live session. `RestartPolicy: Never` is the only thing that closes it, and the staged spec does not say so. Do not weaken that dependency without re-opening this.

FACT: the shipped leak predicate on the retry-placed bind path is the *reservation release* error, not the adapter `Shutdown` outcome. EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2850 (`if relErr := binder.ReleaseSlotReservation(...); relErr != nil { ... slots.MarkLeaked ... health.RecordLeak } else { health.RecordFailure }`). The staged §5.2/§7.1 predicate ("the adapter's `Shutdown` response for that reclaim does not report a clean exit", spec-changes.md:600, :390) is the *new* producer the code lane wires in; the two are additive rather than contradictory, but a reviewer reading only start.go will think the spec cites a predicate the tree does not have.

FACT: `DemoteSDK` does run a real cleanup inside the RPC, so §5.2's "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" (spec-changes.md:601) holds for it. EVIDENCE: pkg/adapter/sdkwarm.go:296-301 → `releaseSessionSlot` at pkg/adapter/slotsession.go:214-220, which deregisters and then `removeSlotTree` synchronously.

WATCHOUT: "a bind refused this way is accounted by the gateway as an ordinary transient slot failure" (spec-changes.md:601) is softer than it reads. At `maxConcurrentSessions: 2` the unhealthy threshold is `ceil(2/2) = 1` (spec/06_warm-pod-model.md:160), so one windowed `RecordFailure` drains the whole pod (pkg/gateway/sessionserver/start.go:2856-2866) — a benign hold race would retire a healthy pod. I did NOT file it, because the proposal's own text says the retries §5.2's policy places never meet the hold (spec-changes.md:190-194), and the three things that can meet it (a §7.4 mid-session upload, a client-driven §7.3 resume, a client retry of the §15.1 start on a create-time-reserved slot) do not run through `applySlotRetryPolicy`, so the sentence's accounting claim is about a path that mostly cannot arise. If a later round wants this, it has to establish which accounting helper the create-time-reserved refusal actually reaches, and that is a code-lane question.

WATCHOUT: a gateway replica dying mid-bind leaves residue that no reclaim was ever *sent* for, so it is neither "a reclaim the adapter does not answer" nor one answered without a clean exit, and nothing counts it toward the §5.2 replacement trigger. Do not file it: it is recorded in Non-goals (summary.md:233-241, the rejected adapter-side pre-start slot lease, "the only trigger that survives a gateway replica dying mid-bind with no gateway participation"), and the Redis-restart-frees-leaked-occupancy sibling is recorded at summary.md:623-640.

USEFUL [Standing context / "§5.2's `**Slot cleanup:**` bullet is concurrency-scoped"]: the concurrency scoping of the bullet at spec/05_runtime-registry-and-pool-model.md:545 and of the `running → failed` / `slot_cleanup → leaked` pair at spec/06_warm-pod-model.md:146-148 is what makes the exclusive-pod arms of SPEC-3 and §7.1 read as deliberate rather than as a missing terminal. It saved me from filing "a pre-`running` cleanup that does not complete on an exclusive pod has no terminal out of `slot_cleanup`" — that hole is pre-existing for the `running → slot_cleanup` edge too and this proposal does not widen it.

UNVERIFIED: whether the compensating `Shutdown`, which the orchestrator's note says runs on `context.WithoutCancel(ctx)`, carries any deadline. §5.2's hold bound falls back to "that request's own deadline when it carries none" (spec-changes.md:601), and a `WithoutCancel` context has no deadline, so on that exact path the stated bound is vacuous. The spec disclaims a reclaim deadline and points at the code ("§5.2's per-slot cleanup timeout is the figure the gateway reuses and the code cites", spec-changes.md:451-452). The non-spec lane should confirm the compensation actually attaches that timeout; if it does not, the hold and the gateway's own wait are both unbounded on the one path the whole proposal exists for.


### [spec-recheck.4.review-security.1]

DECISION: returned an empty findings list for the security lens on the spec staging — BECAUSE every security-shaped claim in the staging verified against the tree, and the two candidate defects I derived both died on evidence (below) — ALTERNATIVES: filing the adapter-self-reported `superseded` outcome as a trust-boundary relaxation of the leak bound; rejected because the leak bound is ALREADY sourced from the adapter's own `Shutdown` response today ("did not report a clean exit"), so the new outcome adds no new channel, and "less strict than it could be" is barred.

FACT: the spec staging has not changed since the spec-recheck-r3 snapshot. `diff` of `spec-changes.md` against `scratchpad/cp-snap/.../spec-recheck-r3` is empty; against `spec-recheck-r2` it is 44 lines, all of them the `running`-boundary rewording ("at the runtime's acknowledgement of it" → "at the moment the session reaches the runtime", and the §6.2 prose's matching change). Round 4's stated delta is therefore r2→r3, not r3→r4. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:22, :90-95, :324, :661

FACT: both shipped-behaviour claims under SPEC-3's widened `**Slot cleanup:**` action list are true. `slotlayout.RemoveTree` removes `CredentialsDir` alongside the slot root, sessions and artifacts trees, best-effort with an accumulated first error. `deregisterSlotLocked` cancels every armed provider expiry timer before deleting the entry. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; pkg/adapter/slotsession.go:174-181

FACT: `lenny_adapter_leaked_slots` is despite its name a GATEWAY-published gauge, registered in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223` and fed from `pkg/sandbox/slotstate`. SPEC-3's sentence putting a gateway-observed leak on that gauge is therefore accurate, and a future reviewer should not file the name as an attribution error. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223; pkg/sandbox/slotstate/registry.go:10,:98

FACT: the adapter connection is dialled per bind attempt and carries one session, so §4.7.1's per-connection epoch latch ("the most recent epoch a response reported to it on one adapter connection") and its per-session phrasing two sentences later ("the epoch the caller holds for that session") coincide in the shipped gateway. `Binder.DialAdapter` is the only construction site and it is called inside each bind path. Do not file the two phrasings as divergent definitions without first showing a multiplexed connection. EVIDENCE: cmd/lenny-gateway/stores.go:2094-2096; pkg/gateway/podlifecycle/podsession/slotbinder.go:237,:459

FACT: the epoch's ABA hazard across an adapter PROCESS restart (counter resets, a fresh entry can carry an epoch a lagging reclaim still names) is closed by the no-re-dial rule rather than by the epoch itself: a restart breaks the connection the compensation is required to run on, so the compensation errors instead of comparing equal against a post-restart entry. That is a second, unstated load for the no-re-dial sentence, and a future reviewer who proposes relaxing it should know it. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:398 (§7.1 "never dials a new one"), :686 (§4.7.1 "never persisted")

FACT: `ABORTED` appears nowhere in `spec/` today. The staged §15.4 reclaim-hold block introduces it as the transient refusal code for this contract, so it is a new statement rather than a reuse of a published mapping; there is no existing spec table it can contradict. EVIDENCE: `grep -rn "ABORTED" spec/*.md` returns nothing; proposals/0081_.../0081_....spec-changes.md:718

WATCHOUT: §7.1's "on a pod serving one session ... the pod retires" survives scrutiny only through the claim-disposition projection, not through §6.2's pre-attached retry bullet, which reads "the pod is marked `failed` and released back to the pool". A terminal claim disposition (`released` or `failed`) projects `draining` then `terminated`, whereas a claim DELETED on a recycling pod projects `idle`; the pre-attached failure sets a disposition rather than deleting the claim, so the pod does retire even on a recycling exclusive pool. EVIDENCE: spec/06_warm-pod-model.md:80; spec/06_warm-pod-model.md:288


### [non-spec-recheck.1.fix-G1.1]

DECISION: split the CODE-6 tier-1 list in place with **S10.**/**S11.** bullet prefixes and a three-step splitting sentence in the section preamble, copying the tier-3 section's existing precedent — BECAUSE the checklist preamble makes each step carry the tests for the tiers its line names, so a case a later deliverable makes pass cannot sit on S9 — ALTERNATIVES: physically moving the bullets into the CODE-1/CODE-2 tier-1 section (that section is scoped to `slotsession_test.go` and `socketruntime_test.go`, the cases live in `bindepoch_test.go`); relaxing the checklist preamble (withdraws the rule that makes the checklist tickable).
FACT: the tier-1 defect reached two bullets the finding did not name. The start-after-the-hold residue case and the start-confirmation-refuses-a-different-epoch case both turn on `noteRuntimeStarted` taking an epoch parameter, which is CODE-2's signature change at S11, so both are now prefixed **S11.** — EVIDENCE: non-spec-changes.md, `### Adapter tests for CODE-6, tier 1`
FACT: `resolvePrepareStagingDir` is `PrepareWorkspace`'s only path into the registry entry and drops everything `ensureSlotPaths` returns except `paths.Staging`, so widening `ensureSlotPaths` alone leaves `PrepareWorkspaceResponse.bind_epoch` permanently zero — EVIDENCE: pkg/adapter/staging.go:133-144, and the sole call at pkg/adapter/staging.go:77-84
FACT: `PrepareWorkspace` resolves lazily, on the first upload frame that carries a session id, and builds its response at `stream.SendAndClose` after the streaming loop — EVIDENCE: pkg/adapter/staging.go:77-84, :116-119. The handler therefore has to hold the epoch beside `stagingDir` across the loop; an upload-free stream resolves nothing and reports zero, which matches SCHEMA-1's "when the plan carries uploads" marking.
WATCHOUT: five shipped test callers break on a widened `ensureSlotPaths` signature and no section of the proposal accounts for them — EVIDENCE: pkg/adapter/exportpaths_test.go:25,221,225; pkg/adapter/slotsession_test.go:319; pkg/adapter/export_test.go:62. This predates the G1 findings and the design explicitly ruled it out of this fix; it needs its own finding, and widening `resolvePrepareStagingDir` adds its own caller set to the same gap.
USEFUL [the design's doNotDo list]: it named the zero-epoch escape-hatch bullet as one to leave on S9 (it must stay green throughout the implementation and asserts teardown rather than the `slot_reclaim` answer), which stopped a fourth reassignment that would have been wrong.


### [non-spec-recheck.1.fix-G2.1]

DECISION: the `ExcludePods` placement append now reads `sbe.Leaked` alone, while the accounting discriminator that chooses `RecordLeak` over `RecordFailure` keeps `sbe.Leaked || relErr != nil` — BECAUSE `relErr` is the gateway-side `ReleaseSlotReservation` failure (a Redis slot-counter and `SandboxClaim` rollback at pkg/gateway/sessionserver/start.go:2833-2848), which leaks occupancy but leaves no residue on the pod, so §5.2's placement constraint, stated in pod-residue terms, does not reach it; narrowing makes the staged code state exactly the predicate SPEC-2's staged §5.2 `**Max retries:**` sentence states — ALTERNATIVES: widening the staged §5.2 sentence to name a failed reservation release as a second exclusion condition (rejected: exports gateway-internal bookkeeping into a reader-facing placement rule, cascades into the §5.2 edge-case list and into every prose site already written at the "unacknowledged reclaim" level, and buys nothing because `ClaimSlot` places only on a genuinely free slot); recording the divergence as an accepted failure mode (rejected: the finding is the divergence); collapsing both predicates to `sbe.Leaked` (rejected: the `relErr` arm of the accounting discriminator is shipped, correct behaviour and dropping it is an unrelated regression).

FACT: the shipped `ReleaseSlotReservation` call inside `applySlotRetryPolicy` takes `(ctx, sbe.Pod, sbe.SlotID)` with no `leaked` argument; the fourth argument the proposal quotes is CODE-4's staged widening. EVIDENCE: pkg/gateway/sessionserver/start.go:2833.

FACT: at `maxConcurrentSessions: 4` the unhealthy threshold is `(4+1)/2 == 2`, so one `RecordLeak` does not drain; that is why the relErr-only arm genuinely leaves the pod a legal placement and why the new tier-1 row is written at concurrency 4. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:215-221; pkg/gateway/sessionserver/start.go:2857.

WATCHOUT: the proposal states the exclusion at two levels. Every site except the two the finding named was already written as "a reclaim that did not complete" / "an unacknowledged reclaim" (non-spec-changes.md field doc-comment bullet and the CODE-6 "unchanged in substance" paragraph; summary.md:216,:659,:678; implementation-checklist.md:30) and stays true under the narrowing. Only the literal Go formula sites drifted. A future edit that restates the predicate should use the abstract wording, which cannot drift from the spec sentence. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md field bullet under "The placement exclusion".

CORRECTS [the review log's "The three leaked discriminators are one predicate across all three bind paths" entry]: that entry is about ACCOUNTING and remains correct there, but it must not be read as covering placement. After this fix the placement half of `applySlotRetryPolicy` is `sbe.Leaked` alone, so accounting and placement are no longer one predicate on that path.


### [non-spec-recheck.1.fix-design-G1.1]

DECISION: CODE-6's tier-1 list is split IN PLACE with **S10.**/**S11.** prefixes, mirroring the tier-3 section's existing split, rather than moving bullets into the CODE-1/CODE-2 tier-1 section — BECAUSE the file already carries that precedent (non-spec-changes.md:1613-1616) and the cases all live in one new file, `pkg/adapter/bindepoch_test.go`, exactly as the tier-3 split keeps one directory across two steps — ALTERNATIVES: physically moving the bullets (splits one narrative across two sections and orphans the "new file" preamble); leaving the list unsplit and relaxing the checklist preamble (withdraws the rule that makes the checklist tickable).

FACT: the tier-1 CODE-6 list spans THREE steps, not two. Besides the three `Shutdown` cases the finding names (S10/CODE-1), two bullets turn on `noteRuntimeStarted` taking a bind-epoch parameter, which is CODE-2 at S11: ":1344-1351" (the start after the hold releases, asserting "the start confirmation … compares its own claim's epoch") and ":1358-1360" ("The start confirmation refuses a different epoch"). EVIDENCE: non-spec-changes.md:1344-1351,1358-1360; implementation-checklist.md:26 (S11 = CODE-2, `noteRuntimeStarted` takes the epoch).

FACT: CODE-6 does NOT edit `pkg/adapter/session.go`. Its file list is bindepoch.go, slotsession.go, holdstate.go, adapterclient/client.go (+ server.go/slot.go for field declarations). So at S9 the hold is taken by `releaseSessionSlot` and the §10.1.4 hold termination only; `Shutdown` starts routing through `reclaimSlotLocked` at S10 with CODE-1. Any tier-1 case that drives the hold must drive it through one of the first two at S9. EVIDENCE: non-spec-changes.md:956 (CODE-6 heading), :988-999 (three sites rule), :107-123 (CODE-1 targets, "hold insertion" in clause two).

FACT: `PrepareWorkspace` reaches the registry only through `resolvePrepareStagingDir(sessionID) (string, error)`, which discards everything `ensureSlotPaths` returns except `paths.Staging`, and it resolves lazily inside the stream loop under `if stagingDir == ""`. A stream carrying no upload frame never resolves and never touches an entry, so `PrepareWorkspaceResponse.bind_epoch` is legitimately zero there — which matches SCHEMA-1's "required … when the plan carries uploads" and the tier-3 case's "on a plan with uploads". EVIDENCE: pkg/adapter/staging.go:77-84,114-118,133-144.

WATCHOUT: widening `ensureSlotPaths` to `(slotlayout.SlotPaths, int64, error)` breaks five shipped test callers that no section of the proposal names: pkg/adapter/exportpaths_test.go:25,221,225, pkg/adapter/slotsession_test.go:319, and the exported seam pkg/adapter/export_test.go:62. The scope-accounting paragraph (non-spec-changes.md:1430-1442) accounts only for CODE-2's fixture churn. Left as a SEPARATE FINDING, not landed in this edit.

UNVERIFIED: whether the CODE-6 tier-1 preamble's "Every case must fail against the tree before this amendment and pass after" is honest for the zero-epoch escape-hatch guard, which is by construction green wherever it compiles. It cannot compile before SCHEMA-1, so the sentence is technically satisfied; a later round may want the preamble to say so. EVIDENCE: non-spec-changes.md:1310-1311,1325-1328.

DEFERRED [none]: no correction derived for a file this loop may not edit.


### [non-spec-recheck.1.fix-design-G2.1]

DECISION: narrow the `ExcludePods` placement append to `sbe.Leaked` alone and leave SPEC-2's staged §5.2 sentence exactly as written — BECAUSE `relErr` is the gateway-side `ReleaseSlotReservation` (Redis counter + SandboxClaim rollback, pkg/gateway/sessionserver/start.go:2833-2848), not the pod-side §7.1 reclaim; a clean reclaim whose reservation release errored leaves nothing on the pod that a retry can collide with, and `ClaimSlot` only places where a slot is actually free, so the pod is a legal placement. Every other site in the proposal already describes the exclusion at the "incomplete/unacknowledged reclaim" level (non-spec-changes.md:82-85, :825-829, :949, :1523-1525; summary.md:216, :659, :678; implementation-checklist.md:30), so narrowing makes the design read as one predicate and falsifies nothing else. — ALTERNATIVES: (a) widen the staged §5.2 sentence to name a failed reservation release as a second exclusion condition — rejected: it exports a gateway-internal Redis/claim bookkeeping failure into a reader-facing §5.2 placement rule, forces a matching edit to spec-changes.md:531 and its edge-case list, and buys nothing operationally; (b) leave both and note the divergence as accepted — rejected, it is exactly the "spec and code state the trigger with different conjuncts" defect the finding names.

FACT: the composite `sbe.Leaked || relErr != nil` is genuinely required for the ACCOUNTING discriminator (`RecordLeak` vs `RecordFailure`), because both conditions leak occupancy. Only the placement half is over-wide. Do not collapse the two into one discriminator. EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2856 (shipped `relErr` arm), proposals/…0081….non-spec-changes.md:774-777.

WATCHOUT: non-spec-changes.md:871-872 ("**When it does not fire.** The discriminator is false when the adapter acknowledged the reclaim and the reservation release succeeded") becomes false under the narrowing and MUST be edited in the same pass — with `sbe.Leaked` alone, the exclusion also does not fire when the release errored. A fixer that edits only :776 and :832 leaves a third contradictory statement behind. EVIDENCE: proposals/…0081….non-spec-changes.md:871

FACT: the tier-1 row at non-spec-changes.md:1523-1525 was already written against `sbe.Leaked`, not against the composite, so it needs an added arm (acknowledged reclaim + failed reservation release → `RecordLeak` fires, second `BindSlot` still carries an empty `ExcludePods`) rather than a rewrite.

FACT: `ExcludePods` exists nowhere in the tree (pkg/, spec/, docs/, schemas/, charts/); it is wholly new to this proposal, so there is no shipped behaviour to reconcile against. Deviations file is empty (3 lines).


### [non-spec-recheck.1.review-applicability.1]

FACT: the delta since the last non-spec convergence is ONLY `summary.md`'s `## Impacts on other proposals` table (four 0080 rows consolidated into one). `diff -ru` of the snapshot against the proposal dir reports only `review-log.md` and `summary.md` as differing; `non-spec-changes.md` and the checklist are byte-identical to the snapshot. EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r5 vs proposals/0081_...
FACT: every count in the new consolidated 0080 row checks out against the tree. `tests/claim-map.json` really carries 76 rows, 20 ABSENT / 24 UNWIRED / 32 WIRED, and 0080 §1.12:163 and §1.18:307-308 really say "seventy-six" / "thirty-two are WIRED". EVIDENCE: tests/claim-map.json; proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:163,:307-308
DEFERRED [proposals/0081_.../0081_....summary.md]: the consolidated 0080 row's "What it must do" cell says only "Re-derive §1.12's denominator as seventy-eight". 0080 §1.18:307-308 carries the SAME denominator ("seventy-six") and a WIRED count ("thirty-two") that SCHEMA-1 also moves (to 34). Judged below the filing bar (0080 is an unstaged draft and the row is advisory), but the cell is incomplete as written.
FACT: every proto field number SCHEMA-1 claims free IS free. `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` on ONE line (schemas/lenny-adapter.proto:1033). WATCHOUT: an `awk '/^message X \{/,/^\}/'` range over that file silently swallows the NEXT message for any empty one-line message, which makes `AssignCredentialsResponse` look like it carries fields 1,2,3,reserved 4,5 (those are `RotateCredentialsRequest`'s). Use `sed -n "$n,/^}/p"` instead. EVIDENCE: schemas/lenny-adapter.proto:1033-1059
FACT: `validateTestFilesMapped` walks tier2..tier12 (tier7a included) and fails any `_test.go` not covered by a spec-map path, a `dir/...` glob, or a `file::Test` entry (the `::Test` suffix is stripped, so the FILE counts as mapped). This is what makes an unnamed new tier-7a file a tier-0 failure. EVIDENCE: cmd/lenny-test/cmd_validate.go:708-792, :125-139
FACT: `TestClaimRegisterAgreesWithTheAdapterProto` only checks a row's `Message.field` spelling when the row's `surface` string CONTAINS `schemas/lenny-adapter.proto`. SCHEMA-1's two staged rows name Go surfaces, so that arm never fires on them. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:67-76
FACT: `deregisterSlot` (the lock-taking wrapper CODE-6 retires) has TWO live test callers nobody in this loop had noticed: pkg/adapter/podmcp_arming_internal_test.go:88 and :245. The proposal's scope accounting for that file names only the three `noteRuntimeStarted` lines 84/185/230. Filed.
MISTAKE: the proposal split its tier-3 cases across S9/S10 because the `Shutdown` epoch comparison is CODE-1's (non-spec-changes.md:1613-1616) but left the tier-1 `bindepoch_test.go` list unsplit, so S9's named tier 1 carries three `Shutdown`-dependent cases. Filed.
UNVERIFIED: whether the tier-7a "start-versus-reclaim race" and "reverse ordering" cases are meant to land in a NEW file or inside an already-mapped one (e.g. podmcp_arming_handoff_test.go, which IS mapped via a `::Test` entry). A future round should get the file named rather than re-deriving the gate consequence.


### [non-spec-recheck.1.review-citations.1]

Citation-audit lens, non-spec recheck round 1. Verdict: no findings.

DECISION: returned an empty findings list — BECAUSE every concrete citation I could extract
mechanically from `.non-spec-changes.md` (69 distinct `path:line` refs), `.summary.md` (52) and
`.implementation-checklist.md` resolved to text that says what the proposal attributes to it —
ALTERNATIVES: filing the `AssignCredentialsResponse` field-1 collision I thought I had found
(see MISTAKE below) and filing the `holdstate.go` "same pass" wording; both were wrong or
below the bar.

FACT: the delta since the `spec-recheck-r5` snapshot is ONLY `summary.md` and `review-log.md`;
`non-spec-changes.md`, `spec-changes.md`, the checklist, the status and the deviations files are
byte-identical to the snapshot. `diff -rq scratchpad/cp-snap/.../spec-recheck-r5 proposals/0081_...`
proves it in one command. The summary delta is exactly one hunk: the four separate 0080 rows in
`## Impacts on other proposals` collapsed into one consolidated row (summary.md:659), per
`[f2.open-decisions.0080-consolidated]` in the review log. Every assertion new to that row was
verified this round and every one holds. — EVIDENCE:
proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.summary.md:659

FACT: the consolidated 0080 row's numbers are live-verified, not copied. `tests/claim-map.json`
holds exactly 76 rows: 32 WIRED, 24 UNWIRED, 20 ABSENT (`python3 -c "import json,collections;
d=json.load(open('tests/claim-map.json')); print(len(d['claims']),
collections.Counter(x['status'] for x in d['claims']))"`). 0080 §1.12 is the twenty-ABSENT entry
(:161-175, "seventy-six" at :163, "Two carry a named owner already" giving the eighteen) and
§1.18 is the twenty-four-UNWIRED entry (:305-309). 0080's `Date` line is 2026-08-31
(:8) and `git log -1` on the file gives `a5476f93e 2026-09-06`. — EVIDENCE:
proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:163,:308

MISTAKE (mine, caught before filing): I nearly filed "SCHEMA-1 puts `bind_epoch` at field 1 of
`AssignCredentialsResponse`, which already declares `session_id = 1`". False. `AssignCredentialsResponse`
is declared as a single-line empty message, `message AssignCredentialsResponse {}` at
schemas/lenny-adapter.proto:1033. An `awk '/^message AssignCredentialsResponse \{/,/^\}/'`
range never sees a closing `}` on that line, so it runs on and prints the NEXT message
(`RotateCredentialsRequest`, :1035-1059) — whose field set (1,2,3,reserved 4,5) looks exactly
like a collision. WATCHOUT for the next agent: do not use an awk brace range over this proto;
use `grep -n "message X"` first and check for the `{}` one-liner form. All nine SCHEMA-1 field
numbers are in fact free: ShutdownRequest 7 (1,2,3,r4,5,6 taken), ShutdownResponse 3 (1,2),
PrepareWorkspaceResponse 3 (1,2), FinalizeWorkspaceResponse 2 (1), ResumeResponse 4 (1,2,3),
ConfigureWorkspaceResponse 2 (1), RunSetupResponse 2 (1), AssignCredentialsResponse 1 (none),
StartSessionResponse 2 (1). — EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: the fix-stage text added at commit `645bd1d67` ("the second attempt's fixes") is the
newest text in `non-spec-changes.md` and it is citation-clean. Everything it asserts checks out:
`TestShutdownMessagePostRemovalDescriptor_spec_4_1` at
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208, `assertFieldSet` at
:259 with the unexpected-field `t.Errorf` at :266 inside :264-267, `reserved 4` / `reserved
"slot_id"` still standing (:223); `assertOneof`'s arm-count pin at
tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:76-77 and the
`CheckpointStart` six-field pin at :151; `isTransientPodClaimError` spanning
pkg/gateway/sessionserver/start.go:3648-3681 with every arm a typed error or sentinel and a bare
`return false` at :3681; `writePodClaimError`'s default arm 503 + `Retry-After` at :212-216 with
`RESUME_FAILED` as the resume caller's `fallbackCode` at :3517;
`resume_setup_demotion_internal_test.go` is `package sessionserver` (:3) and declares
`seedResumingRow` (:23) while `start_test.go` is `package sessionserver_test`;
`ResumeRequest` carries `SessionID`, `MaxConcurrentSessions int32` and `CleanupTimeoutSeconds int`
(pkg/gateway/podlifecycle/podsession/binder.go:602-668); `Server` is declared in
pkg/adapter/server.go:59 and `slotState` in pkg/adapter/slot.go:21, so CODE-6's "those two files
are opened even though they hold none of its logic" is right.

FACT: DOCS-2's anchors are all live. docs/reference/adapter-contract.md:10 is the
"complete reference" sentence, :64 is the `DemoteSDK` row, :75 is the `Shutdown` row, :84 is
`**Scrub responsibilities.**`. The gate it names,
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294), reads the row through
`lineContaining(page, "| \`Shutdown\` |")` (:298) and requires exactly the four substrings the
proposal names (:302-307); DOCS-2's replacement row carries all four, so the gate stays green.
DOCS-1's insertion point is real: the per-slot table is
docs/reference/state-machines.md:232-237 and the `receiving_uploads`→`running` row is :235.

FACT: the "five resolve sites" claim is exact and its span-category table is right.
`ensureSlotStateLocked`/`ensureSlotPaths` have exactly five non-test callers:
pkg/adapter/staging.go:134 (`resolvePrepareStagingDir`, whose CALLER stamps `CategoryPermanent`
at :81), :181 and :337 (each stamping `CategoryPermanent` beside the wrap),
pkg/adapter/slotsession.go:75, pkg/adapter/slotcreds.go:26 (neither stamping). All five wrap
`codes.InvalidArgument`.

FACT: the "three deregister-then-destroy sites" claim is exact.
`deregisterSlotLocked` has three non-test call sites: pkg/adapter/session.go:238 (`Shutdown`),
pkg/adapter/slotsession.go:215 (`releaseSessionSlot`, via `deregisterSlot`) and :389
(`deregisterStartedSessions`, the §10.1.4 hold pass 1 reached from holdstate.go:190).

FACT: `pkg/gateway/podlifecycle/podsession/binder_test.go` is CITED (`:301`, `:1156-1173`) but
correctly absent from the "Files touched on application" test list, because the fixtures it
names (`fakeAssigner` at :296 with its `released` slice at :301, `recordingShutdownAdapter` at
:1156) are only READ from the new cases, which land in `slotbinder_test.go` where
`concurrentAdapter` is declared (:71). Do not file this as a missing edit site.

FACT: `AdapterEvents` has exactly one reference under `pkg/gateway`, the doc comment at
pkg/gateway/runtime/adapterclient/client.go:464, so summary.md:597-600's absence claim holds
on the tree rather than on a register.

USEFUL [standing context / anchor sweep]: the Settled entry saying the sixteen spec anchors all
match byte for byte saved me re-deriving them; I re-checked only the two SPEC-1 anchors
(`grep -c` on each verbatim block in spec/04_system-components.md returned 1 for both) and they
still resolve after the amendment.


### [non-spec-recheck.1.review-client-surface.1]

FACT: this round's delta is summary.md ONLY. `diff -q` of the current
non-spec-changes.md against BOTH `scratchpad/cp-snap/.../non-spec-r2/` and
`scratchpad/cp-snap/.../non-spec-recheck-r1-start/` is empty; only
`summary.md` and `review-log.md` differ from `spec-recheck-r5`. The summary
change is the consolidation of four `0080` rows in `## Impacts on other
proposals` into one row (one-row-per-proposal rule) plus a new §1.12 counts
paragraph. EVIDENCE: proposals/0081_.../0081_....summary.md:659

FACT: the SCHEMA-1 field numbers were re-verified field by field against the
tree and all nine are free, with no reserved collision. ShutdownRequest holds
1,2,3,5,6 and reserves 4/"slot_id" (schemas/lenny-adapter.proto:1609-1636);
ShutdownResponse holds 1,2 (:1665-1668); PrepareWorkspaceResponse 1,2
(:699-702); FinalizeWorkspaceResponse 1 (:761-767); ResumeResponse 1,2,3
(:1433-1447); ConfigureWorkspaceResponse 1 (:1690-1692); RunSetupResponse 1
(:869-871); AssignCredentialsResponse is `{}` (:1033); StartSessionResponse 1
(:958-962). Enum prefix convention matches the sibling `SessionScrubOutcome`
(:438-448). Do not re-run this sweep unless the proto moves.

FACT: `tests/claim-map.json` really does carry 76 rows / 20 ABSENT / 24
UNWIRED / 32 WIRED today, so the summary's new §1.12 paragraph is accurate.
EVIDENCE: tests/claim-map.json (counted with python3); 0080 §1.12 at
proposals/0080_...md:163 and §1.18 at :305-308.

WATCHOUT: 0080 §1.18 also carries the SAME denominator and the WIRED count
("Of its seventy-six rows, thirty-two are `WIRED`",
proposals/0080_...md:307-308). The summary's merged 0080 row says only
"§1.12's denominator moves" and "§1.18's twenty-four `UNWIRED` rows are
untouched", which is true of the UNWIRED count but leaves §1.18's own
"seventy-six"/"thirty-two" stale. I did NOT file it: `proposals/` is not one
of the surfaces criterion (d) covers and the row explicitly stages no edit to
0080. Somebody triaging 0080 should still catch it.

FACT: the only closed-field-set gate over a message SCHEMA-1 opens is the one
the proposal names. I enumerated every tier-3 file using `protoreflect`
(shutdown_recycle, adapter_reportusage, checkpoint_stream,
adapter_generation_fence, adapter_checkpointbarrier, adapter_negotiate,
adapter_session_address, interceptor_proto). Only
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208`
pins a closed set over `ShutdownRequest`/`ShutdownResponse` via
`assertFieldSet` (:259-268). `adapter_session_address`'s three tests iterate
only REQUEST messages for `slot_id` presence/reservation
(:112-175), so the nine RESPONSE-side fields cannot reach them.
`adapter_generation_fence` keys on `coordination_generation` only (:71-83).

FACT: no client SDK, OpenAPI document, CRD, or `schemas/*.json` mirrors the
adapter proto. `grep -rln "lenny.adapter.v1"` outside `pkg/proto` and
`pkg/adapter` returns nothing under `sdks/`, `charts/`, or `docs/`;
`schemas/examples/` holds JSONL, runtime-ops and workspace-plan fixtures only,
no proto fixtures. `docs/reference/adapter-contract.md:682` names the proto as
a published artifact but enumerates no message fields. So SCHEMA-1's parallel
representations are exactly: the proto, the regenerated `pkg/proto/adapter/v1`,
the one tier-3 descriptor pin, and DOCS-2's prose. All four are staged.

FACT: `tests/tier0_static/claim_register_proto_agreement_test.go` only
cross-checks rows whose `surface` contains `schemas/lenny-adapter.proto`
(:66-72) and rows matching `no \`x\` on \`Y\`` (:50). Neither new SCHEMA-1 row
trips it. `claim_register_test.go:406` shows a WIRED row carrying a
`deferral_id` is a refusal case; both new rows are WIRED with no
`deferral_id`, which is correct.

DECISION: filed nothing. BECAUSE every client-facing parallel of the change is
staged and every citation I checked resolved. ALTERNATIVES: I came close to
filing that `docs/reference/adapter-contract.md`'s `ReportSessionScrub` row
(":81", "Report the per-slot cleanup outcome ... at each session release, on a
pod of any concurrency and any recycle setting") becomes unconditional-and-wrong
once SPEC-3 withholds the report on the pre-`running` path, and that DOCS-2's
edit list does not touch it. I dropped it on two grounds. (1) The spec's own
§4.7 `ReportSessionScrub` row (spec/04_system-components.md:692) carries the
identical unconditional wording and is deliberately unedited, so the doc stays
in parity with its spec source rather than drifting from it; the tier-11 parity
gate `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43)
only pins the addressing sentence and is unaffected. (2) It is the same
rule-plus-stated-exception structure the material skeptic already refuted for
§5.2's `**Slot cleanup:**` bullet, and DOCS-2's staged `Shutdown` row states the
exception on the very same page. A future round wanting to revive this must
argue from a harm the spec-side parity does not kill.

UNVERIFIED: SCHEMA-1's two claim-register rows land as `WIRED` at S8, but their
named production readers land later (`Client.BindEpoch` at S9, `Server.Shutdown`'s
epoch comparison at S10, `compensateFailedSlotBind` at S12). No gate checks that
a WIRED row's surface exists, so nothing turns red, but the register states
something false between S8 and S12. I judged it below the bar for a single
implementation run; a reviewer who thinks step-level truth of the register
matters should look.


### [non-spec-recheck.1.review-docs-alignment.1]

DECISION: empty findings list for the documentation-alignment lens on the non-spec staging — BECAUSE the only delta since the `spec-recheck-r5` snapshot is `summary.md`'s consolidation of four 0080 impact rows into one (`diff -rq` reports exactly two differing files, `summary.md` and `review-log.md`; `non-spec-changes.md` is byte-identical), that delta touches no docs surface, and a fresh sweep of `docs/` against every behaviour the staging changes turned up no page that becomes wrong and is unstaged — ALTERNATIVES rejected: (1) re-filing `docs/reference/adapter-contract.md:81`'s `ReportSessionScrub` row ("at each session release") against DOCS-2's new withheld-report clause in the `Shutdown` row, rejected because SPEC-3's append reads "It **also** runs when a bind is abandoned or fails…", which places the pre-`running` cleanup OUTSIDE "session release", so the :81 row stays true post-change and mirrors an unedited spec sentence; (2) `docs/reference/state-machines.md:251`'s `slot_cleanup -> leaked` gloss, barred by the standing Trap at review-log.md:528 and settled STALE at :896; (3) an operator-narrative finding on the new `leaked` cause, dead because no operator page or runbook enumerates leaked-slot or pod-replacement causes.

FACT: the delta this round is summary-only and its two new checkable numbers both verify. `tests/claim-map.json` carries exactly 76 claims, `Counter({'WIRED': 32, 'UNWIRED': 24, 'ABSENT': 20})`, matching summary.md's consolidated 0080 row; proposal 0080 says "seventy-six" at `proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:163,:308`. The row's `claimPodMCPStartLocked` citation also resolves: `pkg/adapter/slotsession.go:87-89` inserts the claimant's entry then gates on `len(s.slots) != 1` at `:109`.

FACT: DOCS-2's two staged surfaces both verify against the tree and against the gate. `docs/reference/adapter-contract.md:75` is the `Shutdown` row, `:81` the `ReportSessionScrub` row, `:84` the `**Scrub responsibilities.**` paragraph DOCS-2 inserts after, and `:64` the `DemoteSDK` row. The gate is `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` at `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311`; it reads the row with `lineContaining(page, "| \`Shutdown\` |")` and requires "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub". All four are present in the staged row, and the two new substrings the staged tier-11 extension asserts ("no other bound session", "a session whose start the adapter has admitted") are present verbatim, as are "bind epoch" and "reclaim hold" in the added block. No gate anywhere reads the `DemoteSDK` row (`grep DemoteSDK tests/tier11_docs/*.go` is empty), so DOCS-2's amendment there is free.

FACT: DOCS-2's `DemoteSDK` claim is true in the tree, which nobody had recorded. `pkg/adapter/sdkwarm.go:296-299` calls `noteRuntimeClosed` then `releaseSessionSlot` for `anyRegisteredSession()`, so the demote really does drop the registry entry and the next bind mints a fresh epoch.

WATCHOUT: DOCS-2's own rationale misstates the page's audience. It writes "so the epoch and the hold reach the third-party adapter author the page is written for", but `docs/reference/adapter-contract.md:10` calls itself the complete reference for "the protocol between the Lenny adapter sidecar and **your runtime binary**" and `:53` says of the gateway-to-adapter RPCs "Your runtime binary never sees them directly". The standing Trap at review-log.md:459 records the same reading. I did NOT file it: the staged block's content is accurate and lands, the rationale sentence is proposal prose that never reaches `docs/`, and the remedy is a rewording. A future round that wants to touch it should reword the rationale, not delete the block.

USEFUL [spec-recheck.1.review-docs-alignment.1, spec-recheck.2.review-docs-alignment.1]: their finding that this lens is nearly inert on 0081 by construction, plus `spec-recheck.2`'s row-by-row map of the eleven accepted-failure-mode rows against landing spec sentences, is what let me scope this pass to the non-spec lane's own `## Edge cases and accepted failure modes` list, DOCS-1, DOCS-2 and a `docs/` sweep rather than re-deriving the spec lane.

FACT: the `docs/` sweep is complete for this staging and can be skipped next round unless a new identifier lands. Behaviour-by-behaviour: the `Shutdown` two-teardown split reaches only `adapter-contract.md:75` (`grep -rn "ReportSessionScrub\|per-slot cleanup\|releases the slot" docs/` returns six sites, five of which restate §5.2's unedited uniformity sentence and stay true); the new §6.2 edge reaches only `state-machines.md`'s per-slot table (DOCS-1); the §7.2/§7.3/§29.4/§4.7.9 edits have no docs mirror at all (`grep -rn "half-claimed\|mid-resume\|snapshot-close" docs/` is empty); SPEC-3's two added cleanup actions have no docs mirror; the `ABORTED` refusal and `concurrent_slots_exhausted` mint no `docs/reference/error-catalog.md` row and the shipped generic rows already cover them.

FACT: `docs/api/internal.md:73-131` carries an illustrative `RuntimeAdapter` proto with a `StopSession` RPC and a three-field `StartSessionResponse` that the shipped `schemas/lenny-adapter.proto` does not have. It is ALREADY divergent before SCHEMA-1, so SCHEMA-1's `bind_epoch` on `StartSessionResponse` does not newly falsify it. Do not file it against 0081; it is a pre-existing gap some proposal should own.


### [non-spec-recheck.1.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the amendment adds was
grepped across spec/, docs/, schemas/ and charts/ and every surface that becomes wrong is
already in an edit list; the one candidate I could substantiate (0080 §1.18's stale
denominator) sits in `proposals/`, not in a spec/docs/schema/chart surface, and dies on the
materiality bar the standing trap on bookkeeping findings records —
ALTERNATIVES: filing the §1.18 item (see UNVERIFIED below), and filing the `PrepareWorkspace`
omission from the `staging.go` files-touched bullet (same bookkeeping class, file is listed).

FACT: the delta since the `spec-recheck-r5` snapshot is ONLY `summary.md` (plus the review
log). `diff -rq` over the snapshot and the proposal directory reports two differing files and
`non-spec-changes.md` is not one of them. The whole change is the `## Impacts on other
proposals` table collapsing four 0080 rows into one. — EVIDENCE:
scratchpad/cp-snap/0081_.../spec-recheck-r5 vs proposals/0081_.../ ; summary.md:655-660

FACT: every factual claim in the new consolidated 0080 row checks out against the tree.
`tests/claim-map.json` is 76 rows / 20 ABSENT / 24 UNWIRED / 32 WIRED; 0080's `Date` line is
2026-08-31 and its last commit is 2026-09-06; `claimPodMCPStartLocked` gates on
`len(s.slots) != 1`; `boundSlotState` answers absent and unbound alike with one
`FailedPrecondition`; `CoordinatorFence` reaches `boundSlotState` and never
`ensureSlotStateLocked`. — EVIDENCE: pkg/adapter/slotsession.go:109, :279-283;
pkg/adapter/coordination.go:116; proposals/0080_...:8

FACT: `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` on ONE line
(schemas/lenny-adapter.proto:1033), so `bind_epoch = 1` is free. An `awk '/^message X \{/,/^\}/'`
range over this file silently swallows the NEXT message for any single-line message and made
me briefly think field 1 collided with `session_id`. Use a brace-depth parser. All nine staged
field numbers are free: ShutdownRequest holds 1,2,3,5,6 with 4 reserved (7 free);
ShutdownResponse 1,2 (3 free); PrepareWorkspaceResponse 1,2 (3); FinalizeWorkspaceResponse 1
(2); ResumeResponse 1,2,3 (4); ConfigureWorkspaceResponse 1 (2); RunSetupResponse 1 (2);
StartSessionResponse 1 (2). — EVIDENCE: schemas/lenny-adapter.proto:1033, :1609-1663

FACT: nothing outside `schemas/lenny-adapter.proto` mirrors the bind-sequence response field
sets. `staged_bytes`, `restored_bytes`, `refusal_reason`, `workspace_plan_warnings` appear in
no spec/ or docs/ page. `docs/api/internal.md` carries a hand-written `RuntimeAdapter` proto
excerpt, but it is already fiction (`StopSession`, `UploadFiles`, a three-field
`StartSessionResponse`) and names no `Shutdown`, so the amendment does not newly falsify it.
— EVIDENCE: docs/api/internal.md:74-100, :120-130

FACT: `cmd/lenny-compliance/schemaassert.go` parses the embedded proto with a regexp scoped to
`enum\s+ErrorCode\s*\{(.*?)\}`, so a new sibling top-level enum cannot disturb it. — EVIDENCE:
cmd/lenny-compliance/schemaassert.go:54-56

FACT: `spec/28` holds no `Shutdown` occurrence at all (`grep -n Shutdown spec/28...` is empty),
and §28.7's artifact register already carries `schemas/lenny-adapter.proto`, so the proposal's
"§28's registers ... adds no row" exclusion is correct. §28.4's claim register is fed by
`scripts/seed-claim-register.py`'s `EXPLICIT` list, whose existing WIRED rows carry exactly the
claim/status/spec_anchor/surface/note keys the two staged rows use. — EVIDENCE:
spec/28_communication-channels.md:1759-1790; scripts/seed-claim-register.py:169-230

FACT: the three sites of the per-slot edge list are spec/06_warm-pod-model.md:151-155,
docs/reference/state-machines.md:234-237 and pkg/sandbox/slotstate/slotstate.go:99-102, and
`slot_assigned` appears nowhere else in spec/, docs/ or pkg/. SPEC-4, DOCS-1 and CODE-3 cover
all three, so there is no fourth mirror to miss. — EVIDENCE: those three files

FACT: the tier-11 gates the docs edits must not break are satisfiable as staged.
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` requires only "end-of-session
teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" — all four are in
DOCS-2's replacement row — and the two new substrings DOCS-2 stages ("no other bound session",
"a session whose start the adapter has admitted") are both present in it.
`per_slot_substate_scope_doc_reconciliation_test.go`'s negative loop compares
`generalSlotEdges` against the CONCURRENT block only, so adding
`receiving_uploads ──→ slot_cleanup` to that slice is safe in both directions. — EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36, :55, :70, :102-108

WATCHOUT: `tests/registers/identifier-senses.yaml` keys entries by the 1-based POSITION of a
retired-spelling site within a file, and it carries five entries for
`docs/reference/adapter-contract.md`. I chased this as a possible DOCS-2 edit site (adding or
removing a retired spelling would renumber the occurrences). It is a dead end for THIS
proposal: none of the naming table's retired spellings (`LifecycleChannel`, `lifecycleChannel`,
`controlchannel`, `lifecyclechannel`, `lifecycle-socket`, `@lenny-lifecycle`, `lifecycle-events`)
occurs in that page today, and DOCS-2's staged text introduces none. A future round that adds
one to a page carrying register entries must renumber them. — EVIDENCE:
tests/registers/identifier-senses.yaml:1-12, :236-249;
spec/28_communication-channels.md:148-158

UNVERIFIED: 0080 §1.18 states "Of its seventy-six rows, thirty-two are `WIRED`"
(proposals/0080_...:307-308). SCHEMA-1's two WIRED rows make that seventy-eight and
thirty-four, but the consolidated impacts row names only §1.12's denominator in its "What it
must do" cell and says of §1.18 only that "its twenty-four `UNWIRED` rows are untouched",
which is true but incomplete. I did not file it: 0080 is a draft proposal rather than a
spec/docs/schemas/charts surface, the proposal stages no edit to it, and the standing trap on
bookkeeping-against-the-index findings records five refutals of this class. Whoever runs the
next open-decisions or summary-cleanup pass can add "and §1.18's denominator and WIRED count"
to that cell for one word of cost.

UNVERIFIED: the tier-7a "start-versus-reclaim race" and "reverse ordering" cases name no target
file, while the tier-7a hold-race case explicitly names a new file and its `tests/spec-map.json`
entry, and the proposal states that tier 7a "is mapped file by file and `validate-maps` fails an
unmapped one at tier 0". If an implementor lands the start-versus-reclaim cases in a new file
rather than in a mapped one, that file needs a spec-map entry the proposal does not stage. I did
not file it because the two cases can land in the already-mapped
`tests/tier7a_load_local/shutdown_drain_gate_race_test.go` and the choice is the implementor's.
Confirmed the mapping is per-file: 48 `tier7a_load_local` paths in tests/spec-map.json, each a
file (a few with a `::TestName` suffix). — EVIDENCE: tests/spec-map.json:606-637, :1074, :1393


### [non-spec-recheck.1.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor/action assignment in the staging resolved against the tree, and the round's actual delta (summary.md's four 0080 impact rows consolidated into one) introduced only verifiable citations — ALTERNATIVES: filing the Files-touched-list omission (see DEFERRED below), rejected as bookkeeping that misleads no implementor because SCHEMA-1's field table is the authority.

FACT: the only delta since the spec-recheck-r5 snapshot is in `summary.md`'s `## Impacts on other proposals`: the four `0080 …` rows became one consolidated row. `non-spec-changes.md`, `spec-changes.md` and the checklist are byte-identical to that snapshot. `diff -rq scratchpad/cp-snap/…/spec-recheck-r5 proposals/0081_…` shows only review-log.md and summary.md differing. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r5

FACT: every numeric claim in the new consolidated 0080 row checks out. `tests/claim-map.json` holds exactly 76 rows, 32 WIRED / 24 UNWIRED / 20 ABSENT, so SCHEMA-1's two rows do make §1.12's denominator seventy-eight. EVIDENCE: tests/claim-map.json (python3: len(d['claims'])==76, Counter matches)

WATCHOUT: `awk '/^message X {/,/^}/'` over `schemas/lenny-adapter.proto` gives the WRONG body for a one-line empty message. `message AssignCredentialsResponse {}` is on a single line (schemas/lenny-adapter.proto:1033), so the awk range runs on into `RotateCredentialsRequest` and makes it look like the message already declares fields 1,2,3,5. The proposal's "AssignCredentialsResponse is an empty message today, so its first field is 1" is CORRECT. Use `grep -n "^message X {" ` + an explicit end line instead. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: all nine SCHEMA-1 field numbers are free on their messages, verified by bounded extraction — ShutdownRequest holds 1,2,3,5,6 with 4 reserved (1609-1636); ShutdownResponse 1,2; PrepareWorkspaceResponse 1,2; FinalizeWorkspaceResponse 1; ResumeResponse 1,2,3; ConfigureWorkspaceResponse 1; RunSetupResponse 1; StartSessionResponse 1; AssignCredentialsResponse empty. EVIDENCE: schemas/lenny-adapter.proto:699,761,958,1033,1433,1609,1665,1690

FACT: the tier-0 gates over the adapter proto are safe for these additions. `TestAdapterProtoAddressesASessionOneWay` only cross-checks the `session_id`/`SessionId` name-type pair and stream-envelope frames (tests/tier0_static/adapter_proto_message_scope_test.go:75-113); `adapter_proto_generation_scope_test.go` pins doc-comment text at named anchors; `claim_register_proto_agreement_test.go` tracks only `coordination_generation`. None fires on a bare `int64 bind_epoch`. EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:33-49

FACT: the only closed field-set pin over the messages SCHEMA-1 opens is `TestShutdownMessagePostRemovalDescriptor_spec_4_1` (tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208, `assertFieldSet` at :259, unexpected-field arm at :264-267). A grep of tests/ and cmd/lenny-compliance for the seven response messages returns nothing else. The proposal's claim is exact. EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-237

FACT: `pkg/adapter/AssignCredentials` (credentials.go:65) only delegates; the `&adapterv1.AssignCredentialsResponse{}` literal is built in `assignCredentialsSlot` at pkg/adapter/slotcreds.go:52, which is why the proposal's Files-touched list correctly names `slotcreds.go` and not `credentials.go` for that epoch. EVIDENCE: pkg/adapter/credentials.go:74; pkg/adapter/slotcreds.go:52

FACT: the five production `ReleaseSlotReservation` call sites are exactly the five the proposal's table names, plus the interface decl — start.go:2834 (applySlotRetryPolicy), start.go:3246 (rollbackClaim), slotbinder.go:172 (ClaimSlot connect stage), slotbinder.go:217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), interface at start.go:2727. EVIDENCE: pkg/gateway/sessionserver/start.go:2727,2834,3246

FACT: `podRegistry.Put` has exactly the three production callers the CODE-6 latch argument names: start.go:2936, start.go:4057, cmd/lenny-gateway/coordination_seams.go:249. EVIDENCE: pkg/gateway/sessionserver/start.go:2936,4057

FACT: the §10.1.4 coordinator-hold allowlist has FIVE entries, not three: CoordinatorFence, NegotiateVersion, AdapterEvents, and both grpc.health.v1 methods. The accepted-failure-mode bullet's "the fence, version negotiation, the event stream, and the health probes" is accurate and `Shutdown` is genuinely refused there. EVIDENCE: pkg/adapter/holdstate.go:52-58

FACT: tier 10 already drives the exported adapter `Server` directly — `recycle_scrub_conformance_test.go` says so in its own header and imports `pkg/adapter`. CONF-1's home is precedented, so "tier 10 is a runtime-adapter battery only" is not an objection. EVIDENCE: tests/tier10_conformance/recycle_scrub_conformance_test.go:12-31

FACT: §15.4 is "Runtime Adapter Specification" and spec/15:1469's SDK-warm demotion contract is an adapter obligation ("Adapters ... must implement the DemoteSDK RPC"), so SPEC-5's adapter-facing bind-epoch and reclaim-hold blocks sit with the right actor. EVIDENCE: spec/15_external-api-surface.md:1458,1469

FACT: the tier-11 anchors DOCS-1's test work cites all resolve — `generalSlotEdges` at per_slot_substate_scope_doc_reconciliation_test.go:32-37, positive loop at :55, negative loop at :70, page `requireAllContain` at :102-108 — and `generalSlotEdges` has no third reader. The shipped `docs/reference/state-machines.md` per-slot table has four rows at :234-237 with the `receiving_uploads → running` row at :235, which is where DOCS-1's row goes. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32,55,70,102

FACT: `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` reads the row via `lineContaining(page, "| \`Shutdown\` |")` and requires exactly the four substrings the proposal names; the staged one-line row carries all four plus the two DOCS-2 adds. `docs/reference/adapter-contract.md` line numbers :10, :64 (DemoteSDK), :75 (Shutdown), :84 (Scrub responsibilities) all resolve. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:298-307

DEFERRED [0081_…non-spec-changes.md]: the `## Files touched on application (non-spec)` list under-describes two of the seven epoch-reporting handlers. `pkg/adapter/staging.go`'s entry names "the epoch on the `FinalizeWorkspace` and `RunSetup` responses" but not `PrepareWorkspace`'s (built at pkg/adapter/staging.go:116), and `pkg/adapter/session.go`'s entry does not name the epoch on `StartSessionResponse` (built at pkg/adapter/session.go:164). Both files are already in the list and SCHEMA-1's field table names both fields, so no implementor is misled; I judged it below the bar rather than filing it. If a later pass wants it closed, the fix is two clauses in those two bullets.

UNVERIFIED: the CODE-4 claim that `status.Code` resolves through `fmt.Errorf("%w")` + `*SlotBindError` by walking the chain with `errors.As`. I read the grpc `status` package's behaviour from the proposal rather than from the vendored source. Whoever implements S12/S13 should pin it with a test rather than trust the sentence. EVIDENCE: proposals/0081_…/0081_….non-spec-changes.md CODE-4, "because `status.Code` walks the chain with `errors.As`"

USEFUL [Standing context: "every deregister-then-destroy site goes through `reclaimSlotLocked`, and there are three"]: this saved me from re-deriving the hold's call-site set. Confirmed against the tree: `deregisterSlotLocked` has exactly three destroy-adjacent reachers — session.go:238, slotsession.go:215 (via deregisterSlot:192), slotsession.go:389 (deregisterStartedSessions). EVIDENCE: pkg/adapter/slotsession.go:174,192,215,389


### [non-spec-recheck.1.review-fresh.1]

FACT: the delta this recheck was launched for is TINY. `diff -ru` between
`scratchpad/cp-snap/.../spec-recheck-r5` and the proposal directory touches only two files, and
`non-spec-changes.md` and `spec-changes.md` are byte-identical to the snapshot. The only staged-content
change is `summary.md`'s `## Impacts on other proposals`, where four 0080 rows were consolidated into
one. Everything else in the diff is two new review-log entries (`f2.open-decisions.0080-consolidated`,
`f2.summary-cleanup`). Do not spend a round re-reading the whole staging looking for the change.
EVIDENCE: diff -rq scratchpad/cp-snap/0081_.../spec-recheck-r5 proposals/0081_...

FACT: the consolidated 0080 row's new arithmetic is exact against the tree. `tests/claim-map.json`
holds 76 claims: 32 WIRED, 24 UNWIRED, 20 ABSENT (counted with python this round). 0080 states
"seventy-six" at its :163 and :307-308. The `Date: 2026-08-31` / `a5476f93e 2026-09-06` pair is right.
EVIDENCE: tests/claim-map.json; proposals/0080_...:8,:163,:307-308

FACT: all nine SCHEMA-1 field numbers are free on their messages, re-verified by reading the proto this
round. `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1609-1636);
`ShutdownResponse` 1,2; `PrepareWorkspaceResponse` 1,2; `FinalizeWorkspaceResponse` 1; `ResumeResponse`
1,2,3; `ConfigureWorkspaceResponse` 1; `RunSetupResponse` 1; `AssignCredentialsResponse` empty;
`StartSessionResponse` 1. The only closed field set over an opened message is
`TestShutdownMessagePostRemovalDescriptor_spec_4_1` (`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208`,
`assertFieldSet` at :259, the unexpected-field error at :264-267), exactly as SCHEMA-1 says.
`tests/tier3_contract/interceptor_proto/contract_test.go:38` is a second exact-field-set helper but it
is over `lenny-interceptor.proto`, so it is not a hazard here.
EVIDENCE: schemas/lenny-adapter.proto:1609-1636; tests/tier3_contract/interceptor_proto/contract_test.go:38-56

FILED [prepare-workspace-epoch]: `PrepareWorkspaceResponse.bind_epoch` has no route to a value.
CODE-6 widens only `ensureSlotPaths`, but `PrepareWorkspace` reaches the entry solely through
`resolvePrepareStagingDir`, which returns `(string, error)` and drops everything but `paths.Staging`
(`pkg/adapter/staging.go:133-144`), and the response is built at `staging.go:114-118`. The
`Files touched` line for `staging.go` names only the FinalizeWorkspace and RunSetup responses
(non-spec-changes.md:2007-2008). This is the same defect an earlier round recorded as FILED at
review-log.md:453 and archive:18583; it was never landed. Whoever fixes it must widen
`resolvePrepareStagingDir` too and add PrepareWorkspace's response to the staging.go file line.

FILED [credential-lease-release]: CODE-4's `b.releaseCredentials(req.SessionID)` in the
`materializeSlot` wrapper destroys a concurrent successor attempt's leases. `Service.ReleaseSession`
iterates `LeasesBySession([sessionID])` and releases every lease under the key
(`pkg/gateway/credentials/credassign/credassign.go:400-409`), while `assignSlotCredentials` mints per
attempt under the same key. Re-derived independently this round; it matches the standing FILED/OPEN
entries at review-log.md:732 and :842, which are still open. The proposal states the correct rule for
the resume path (non-spec-changes.md:701-704, "would leave every later resume of that session running
with leases the gateway has already released") and does not carry it to the bind path.

USEFUL [review-log.md:836]: the "does `ensureSlotPaths` widening break `exportpaths_test.go`?" Open
saved me from filing a build-adaptation item. `pkg/adapter/exportpaths_test.go:25,:221,:225` is still
absent from the file lists; still judged below the bar.

WATCHOUT: `Client.Shutdown` at `pkg/gateway/podlifecycle/podsession/slotbinder.go:542` and
`binder.go:2043` send the unconditional form and remove the entry without clearing the connection's
epoch latch. Both close the connection immediately afterwards (`defer result.Adapter.Close()`), which
is why it is harmless today; a later change that keeps the connection open across an ordinary
`Shutdown` reopens it. Recorded as UNVERIFIED once before at archive:18583.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542-544

FACT: every other load-bearing code citation I spot-checked in CODE-4/CODE-5 resolves.
`materializeSlot` at slotbinder.go:265 with exactly five `cl.Close()` stage branches;
`connectSlot`'s `podclaim.SlotRequest` mapping at slotbinder.go:423-429; `expiredByUptime` at
slotclaimer.go:433 and :491; `bindConcurrentSlot` at start.go:2594-2609; `resumeOnPod`'s Resume
failure branch at start.go:4041; `isTransientPodClaimError` at start.go:3648-3681 with the final
`return false` at :3681; `SlotBindError.Reason()`'s transient default at slotfailure.go:100-101;
`ResumeRequest` carries `MaxConcurrentSessions` and `CleanupTimeoutSeconds`
(binder.go:645,:667); `DemoteSDK` really does drop the entry via `releaseSessionSlot`
(sdkwarm.go:296-301); the five `ensureSlotStateLocked` resolve sites are exactly the stated five.


### [non-spec-recheck.1.review-kubernetes.1]

FACT: the Kubernetes surface of this proposal is small and already idiomatic; I found no
idiom violation. The three places it touches Kubernetes all check out against the tree:
(1) the unhealthy-threshold drain is routed through the gateway-stamped
`lenny.dev/drain-request` Pod ANNOTATION and the WarmPoolController writes `Sandbox.status`,
so no non-owning component writes another's status — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:583-603; spec/04_system-components.md:418,:632.
(2) The new `ExcludePods` field is a read-only placement filter in `ClaimSlot`'s two candidate
passes, mirroring the shipped `expiredByUptime` skip, which the code comments explicitly mark
as "read-only placement filter; the gateway skips the pod without writing Sandbox.status" —
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:433-440,:491-499. (3) The proposal
itself states that the drain is asynchronous and therefore does NOT keep the immediate retry
off the pod, which is why the exclusion exists — a correct reading of level-triggered
reconciliation, not a fight with it (non-spec-changes.md, "Reachability" bullet under CODE-5).
The bind epoch is pod-local, in-process, never persisted, so etcd is not used as a message bus
or per-request store. No finalizer, no admission webhook, no field-manager contention.

FACT: `terminalReclaimPreRunning` → `ReclaimClaimed` → `podclaim.DeleteClaim` is real and the
summary's citations for it resolve. EVIDENCE: pkg/gateway/sessionserver/usage.go:446,:581-605;
pkg/gateway/podlifecycle/podsession/binder.go:1097-1106.

FACT: `ReleaseSlotReservation` has exactly five production call sites plus the `slotBinder`
interface declaration, and CODE-4's six-row call-site table matches them one for one.
EVIDENCE: pkg/gateway/sessionserver/start.go:2727,:2834,:3246;
pkg/gateway/podlifecycle/podsession/slotbinder.go:172,:217; binder.go:1714.

WATCHOUT: `tests/tier4_integration/recycle_scrub_path_test.go`'s `recycle-pool` fixture holds
exactly ONE Sandbox, `sbx-r`, created in `recycleCluster`. No other `lennyv1.Sandbox` is
created anywhere in the file (the only two `Name: "sbx-r"` sites are that Sandbox and a
corev1.Pod of the same name for the recycle coordinator). Any staged case that needs a
placement alternative on that pool has to create a second Sandbox and seed its status
subresource, exactly as `recycleCluster` does at :193-199 — Create ignores status.
EVIDENCE: tests/tier4_integration/recycle_scrub_path_test.go:153-201,:246.

UNVERIFIED: the same tier-4 "datastore-crossing case" also asserts the leak reaches
`RecordLeak` and the `lenny_adapter_leaked_slots` gauge. Both live in
`pkg/gateway/sessionserver`'s UNEXPORTED `accountSlotFailure`/`applySlotRetryPolicy`
(pkg/gateway/runtime/slothealth/slothealth.go:121; pkg/gateway/sessionserver/start.go:2806),
while that fixture stands up only a `podsession.Binder`. Whether an external test package can
drive that accounting at all on this fixture is unchecked; a test-coverage lens should look.

DECISION: I filed only the "second placeable pod" claim, BECAUSE it is a flatly checkable
false statement about the tree that an implementor would build a failing test on.
ALTERNATIVES: I considered filing the whole tier-4 datastore-crossing case as unbuildable on
that fixture, and did not, because "Extend" leaves the fixture wiring open and I could not
show the remaining halves are impossible rather than merely more work than the text implies.


### [non-spec-recheck.1.review-mechanism.1]

FACT: the delta since the `spec-recheck-r5` snapshot is ONLY `summary.md`'s
`## Impacts on other proposals` 0080 rows (four collapsed into one) plus review-log
appends. `diff -rq` shows `non-spec-changes.md`, `spec-changes.md`, the checklist,
the problem statement and the status file byte-identical. Do not spend a round
re-diffing; the consolidated 0080 cell is the only new prose.
EVIDENCE: scratchpad/cp-snap/0081_.../spec-recheck-r5 vs proposals/0081_...

FACT: every claim in the consolidated 0080 cell that is checkable against the tree
checks out, verified this round. `tests/claim-map.json` holds exactly 76 rows,
20 ABSENT / 24 UNWIRED / 32 WIRED (counted with python, not grep).
`claimPodMCPStartLocked`'s `len(s.slots) != 1` gate is at pkg/adapter/slotsession.go:108;
`boundSlotState` is at pkg/adapter/slotsession.go:274-283; `CoordinatorFence` resolves
through `boundSlotState` at pkg/adapter/coordination.go:116. Do not re-derive these.

FACT: ALL NINE SCHEMA-1 field numbers are free, re-verified by descriptor walk.
ShutdownRequest holds 1,2,3,reserved 4,5,6 (schemas/lenny-adapter.proto:1610-1635) so 7
is free; ShutdownResponse holds 1,2 (:1666-1667); PrepareWorkspaceResponse 1,2 (:700-701);
FinalizeWorkspaceResponse 1 (:767); ResumeResponse 1,2,3 (:1434-1446);
ConfigureWorkspaceResponse 1 (:1691); RunSetupResponse 1 (:870);
StartSessionResponse 1 (:961); `AssignCredentialsResponse` really is `{}` at :1033.
WATCHOUT: an `awk '/^message X \{/,/^}/'` range does NOT work on this file — awk reads
the brace as an ERE interval and silently matches a different message, which is how I
first "found" AssignCredentialsResponse carrying five fields. Use a brace-depth walk in
python. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: the tier-3 closed-field-set gate the proposal names is exactly as described.
`TestShutdownMessagePostRemovalDescriptor_spec_4_1` is at
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208 with
`assertFieldSet` at :259 erroring on any undeclared number at :264-267; the
`ShutdownResponse` call is at :233. The sibling `checkpoint_stream` pins
(`assertFields` :92, CheckpointStart six fields :151, `assertOneof` :77) are over
messages SCHEMA-1 does not open. No other closed-field-set gate exists over adapter
proto messages: `tests/tier3_contract/interceptor_proto/contract_test.go:38` is over
`schemas/lenny-interceptor.proto`. `make generate-proto` emits only `pkg/proto`
(Makefile:91-100), so `sdks/` needs no regeneration.

FACT: the tier-11 gate claims hold. `generalSlotEdges` is the four-entry slice at
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, feeding a
positive loop over a `generalBlock` that runs to the END of §6.2 and a negative loop over
a `scopedBlock` bounded above by the general header, so one added entry really does gate
both sides. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` is at
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294 with the four
substrings at :302-307, and all four survive DOCS-2's staged row.
`docs/reference/state-machines.md:232-237` uses the exact `| \`a\` | \`b\` | trigger |`
spacing DOCS-1's row assumes.

FACT: `status.Code` in grpc v1.80.0 unwraps with `errors.As`
(~/go/pkg/mod/google.golang.org/grpc@v1.80.0/status/status.go `FromError`), so CODE-5's
`codes.Aborted` arm really does fire through the `*SlotBindError` wrapper. And
`SlotBindError.Reason()` (pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101)
switches on the CODE alone except for a `FailedPrecondition`+workspace-stage special
case, so `Aborted` takes the transient default from EVERY stage, credential stage
included. No shipped test asserts an `Aborted` classification, so the new arm breaks none.

FACT: no division-by-zero in `slotCleanupBudget`. Every `materializeSlot` caller is gated
on `MaxConcurrentSessions > 1`, and the resume caller clamps through
`maxConcurrentSessions()` at pkg/gateway/sessionserver/start.go:3353-3358, used at :4029.

WATCHOUT: `releaseSessionSlot` (pkg/adapter/slotsession.go:214-220) is fully synchronous,
so CODE-6's hold on the `DemoteSDK` / start-rollback paths is released before those RPCs
answer. A future round worrying that the hold blocks the pod-warm fallback after
`DemoteSDK` can stop there.

FILED: the `ExcludePods` discriminator is `sbe.Leaked || relErr != nil`
(non-spec-changes.md:776, :832) while the staged §5.2 `**Max retries:**` sentence
(spec-changes.md:531) places a retry on a different pod only when the pod "holds such an
incomplete reclaim". A clean reclaim whose gateway-side `ReleaseSlotReservation` errored
is neither saturated, nor (at `maxConcurrentSessions >= 3`) unhealthy, nor holding an
incomplete reclaim, yet the code excludes it. The standing context already records this
as a FACT ("a third arm no spec sentence names", review-log.md:149) and the round that
broadened the spec from "unacknowledged" to "did not complete" (review-log.md:2044)
addressed only two of the three arms.

FILED (re-file of a live Open item, review-log.md:732 and :842): CODE-4's
`b.releaseCredentials(req.SessionID)` (non-spec-changes.md:652) is keyed on the SESSION,
and `credassign.Service.ReleaseSession` releases every lease under that key
(pkg/gateway/credentials/credassign/credassign.go:400-409) from the store both the pool
and the user minters write into. A concurrent second `/start` for the same session —
which the proposal itself records as unserialized — is past `assignSlotCredentials` when
attempt 1's wrapper runs, so the epoch fences the pod-side reclaim to `superseded` and the
next statement destroys the successor's leases anyway. It was filed in an earlier window
and is still unstaged; I re-filed it because the orchestrator's fixed/refuted lists do not
carry it.

UNVERIFIED: the staged §15.4 conformance clause "An adapter does not conform when it ...
reports zero on a bind-sequence response" (spec-changes.md, SPEC-5 §15.4 block) may be
falsified by the graceful-refusal arms the proto already documents:
`ConfigureWorkspaceResponse.refusal_reason` (schemas/lenny-adapter.proto:1691) and
`StartSessionResponse.refusal_reason` (:961) both describe a successful response on a path
that need not have created an entry. Neither is ever populated in the shipped tree
(`grep -rn RefusalReason pkg/ cmd/` returns nothing), so I did not file it. A
client-surface or conformance lens should decide whether the absolute clause wants a
carve-out for a graceful refusal.

UNVERIFIED (bookkeeping only, deliberately not filed): `ensureSlotPaths` widens its return
under CODE-6, and `pkg/adapter/exportpaths_test.go:25,:221,:225` calls it but appears in no
file list. Same class: `StartSessionResponse.bind_epoch` is in the SCHEMA-1 table and in
CODE-6's "seven handlers" sentence but in neither CODE-6's file list nor the
`pkg/adapter/session.go` entry of "Files touched on application". Both are compile-time
obvious.


### [non-spec-recheck.1.review-operational.1]

Lens: operational consistency (conditions, metrics, alerts, operator docs). Returned zero findings.

FACT: the delta since the `spec-recheck-r5` snapshot is TWO files only — `summary.md` and
`review-log.md`. `diff -rq` over the snapshot proves it; `non-spec-changes.md`,
`spec-changes.md` and the checklist are byte-identical to the snapshot. The whole summary
delta is one hunk: the four separate 0080 rows in `## Impacts on other proposals` collapsed
into one consolidated row. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-recheck-r5/

FACT: every checkable citation in that consolidated 0080 row verifies.
`tests/claim-map.json` holds exactly 76 claims, 32 WIRED / 24 UNWIRED / 20 ABSENT
(`python3 -c "import json,collections; ..."`), so the "denominator becomes seventy-eight"
arithmetic holds. `claimSessionSlotUnderLock` inserts through `ensureSlotStateLocked` at
pkg/adapter/slotsession.go:75 and `claimPodMCPStartLocked` gates on `len(s.slots) != 1` at
:108. `boundSlotState` answers absent and unbound alike with one `codes.FailedPrecondition`
at pkg/adapter/slotsession.go:274-283, and `CoordinatorFence` resolves through it at
pkg/adapter/coordination.go:116.

FACT: there is NO alert on any slot metric. `pkg/alerting/rules/rules.go` contains no
occurrence of "slot", and no file under `docs/runbooks/` names a slot leak. The
alert-to-runbook half of the operational lens is vacuous for this proposal; do not spend a
round re-deriving it.

FACT: the metric surface this proposal touches is three shipped gateway series and it adds
none. `lenny_adapter_leaked_slots{pod_id,pool}` (gatewaymetrics_credential.go:219-223),
`lenny_slot_pod_replacement_total` (`slotReplacement func(pool string)`,
sessionserver.go:412-416) and `lenny_slot_failure_total{error_type,pool,k8s_pod_name}`
(recordSlotFailure, slotbinder.go:353-360). The four `error_type` values are emitted from
inside the body that CODE-4 renames to `materializeSlotStages`, so they travel with the move
and no new label value is minted. `docs/reference/metrics.md` lists neither
`lenny_adapter_leaked_slots` nor any per-slot state series, so the docs-mirror half is also
vacuous.

FACT: a pre-existing label divergence, NOT this proposal's: spec/16_observability.md:15 and
docs/reference/metrics.md:167 both label `lenny_slot_pod_replacement_total` with
`pool, k8s_pod_name`, while the shipped hook is `slotReplacement func(pool string)` —
one label. The proposal adds two callers of that hook and changes neither side. Anyone
tempted to file it must file it against the tree, not against 0081.

FACT: the tier-11 gate arithmetic in the staging checks out end to end. The four substrings
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307) all survive
DOCS-2's replacement row, and the two the proposal adds ("no other bound session",
"a session whose start the adapter has admitted") are both literally present in it. The
page-level additions "bind epoch" and "reclaim hold" are present in lowercase inside DOCS-2's
new block, which matters because `requireAllContain` is case-sensitive. On the other gate,
adding `receiving_uploads ──→ slot_cleanup` to `generalSlotEdges`
(per_slot_substate_scope_doc_reconciliation_test.go:32-37) is safe on BOTH loops: the
positive loop reads the either-concurrency block (:55) which SPEC-4 edits, and the negative
loop reads the scoped block (:70), which contains only `running ──→ failed` and
`slot_cleanup ──→ leaked` and so cannot match the new string.

WATCHOUT: `docs/reference/adapter-contract.md:81`'s `ReportSessionScrub` row says the outcome
is reported "at each session release", which looks like it collides with SPEC-3's
withheld-report rule and is NOT in DOCS-2's edit list. It does not collide: SPEC-3 defines the
pre-`running` reclaim out of "session release" ("A cleanup that reclaims a slot the pod's
shared runtime process was given is a session release like any other and reports its
outcome"), and spec/04:692 and spec/12:481 use the same wording for the same boundary. I
chased this to the bottom and it is not a finding. EVIDENCE: docs/reference/adapter-contract.md:81;
spec/04_system-components.md:692; spec-changes.md:600 (SPEC-3 append).

WATCHOUT: the same shape one level down — `docs/reference/state-machines.md:251` and
spec/06_warm-pod-model.md:148 both state the `slot_cleanup → leaked` trigger narrowly as
"cleanup timeout exceeded", while SPEC-3 adds a leak on a reclaim answered without a clean
exit. That divergence is PRE-EXISTING: spec/05:545 already says "If cleanup fails, the slot is
leaked" and the shipped `Binder.ReleaseSlot` already sets `leaked = err != nil || !cleanly`.
Filing it against 0081 needs an argument that 0081 is what makes it wrong, and I could not
build one.

UNVERIFIED: CONF-1 stages four tier-10 properties, all bind-epoch, and S14 scopes itself to
"§15.4's published bind-epoch contract" — but SPEC-5 publishes TWO §15.4 blocks, and the
second (the slot-identifier reclaim hold, with its own `ABORTED` non-conformance statements at
spec-changes.md:713) gets no tier-10 property. I did not file it: the hold's refusal and its
`codes.Aborted` code are pinned at tier 1 (non-spec-changes.md:1354) and tier 7a (:1771), so
"names no concrete test" fails, and a near-identical "add a fifth CONF-1 property" finding is
already on the refuted list. Somebody deciding whether the third-party conformance battery
should cover the whole of §15.4 rather than half of it should decide it as a scope question,
not as a defect.


### [non-spec-recheck.1.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the delta since this lane's last convergence
is a pure bookkeeping consolidation of four `## Impacts on other proposals` rows for 0080 into
one, and every load-bearing number in the new text verified true against the tree.
ALTERNATIVES: I considered filing (a) the synchronous, `context.WithoutCancel`-detached
compensation adding up to `slotCleanupBudget` per failed attempt to a client-held request goroutine,
and (b) the exclusive-pool resume budget degenerating to the whole `cleanupTimeoutSeconds`
(30–60s in spec/05's own examples). Both are already recorded as standing Open items that three
prior performance lenses declined to file, on the ground that no stated budget is breached and
the proposal argues the direction explicitly (review-log.md:787, :798). Re-filing them would
re-litigate a workable design choice.

FACT: the delta is confined to `summary.md` and `review-log.md`. `diff -rq` against
`scratchpad/cp-snap/.../spec-recheck-r5` reports only those two files differ; the whole
`non-spec-changes.md` is byte-identical to the text the last non-spec lane converged on.
EVIDENCE: proposals/0081_.../0081_....summary.md:659 (the single consolidated 0080 row).

FACT: the consolidated row's arithmetic is correct as of today. `tests/claim-map.json` holds
exactly 76 `"status"` rows, 20 `ABSENT`, 24 `UNWIRED`, 32 `WIRED`, counted this pass.
EVIDENCE: tests/claim-map.json (grep -c '"status"' → 76).

FACT: the two code citations the consolidated row newly leans on both resolve.
`claimPodMCPStartLocked` gates the pod-wide MCP arming on `len(s.slots) != 1`
(pkg/adapter/slotsession.go:108) after `claimSessionSlotUnderLock` has already inserted the
claimant's own entry through `ensureSlotStateLocked` (:75), so two ordinary interleaved binds
miss the arming with no failed bind involved. `CoordinatorFence` and `CheckpointBarrier` both
resolve through `boundSlotState` (pkg/adapter/coordination.go:116, :253), which never reaches
`ensureSlotStateLocked`, so the staged `errSlotReclaimInProgress` refusal cannot surface on
either — the row's claim that the hold adds no fourth class to 0080 §1.19 holds.

FACT: the amendment adds no control-plane or data-plane write of any kind. The bind epoch is an
in-process `int64` on `Server` guarded by `s.mu` and never persisted; the reclaim hold is a
`map[string]struct{}` on the same struct; the gateway side is a per-connection `atomic.Int64`
latch. No etcd object, no Postgres row, no Redis key, no informer or watch is added, so the
write-amplification, hot-key and informer-memory arms of this lens are all vacuous for this
change. EVIDENCE: non-spec-changes.md:955-1010 (CODE-6).

FACT: the hold does not serialize the adapter. It is a map entry, not a held mutex, and CODE-1
explicitly releases `s.mu` before the drain, the `Runtime.Close`, `removeSlotTree` and the scrub
report. Because `slotId == sessionId`, a held identifier blocks only further attempts at the same
session, never another tenant's bind. EVIDENCE: non-spec-changes.md:127-135, :215-232.

WATCHOUT: the §12.4 durable-fallback arm of this lens looks like an open hole and is not one for
this proposal. The absence of durable backing for a leaked slot's Redis occupancy is analysed at
length under `## Defects in the shipped tree that this proposal does not stage`, including the
four restatement sites and the `migrations/0080_sessions_active_by_pod_index.up.sql` predicate
mismatch, and is correctly scoped out as pre-existing rather than introduced here.
EVIDENCE: summary.md:623-655.

USEFUL [review-log.md:754-839]: the standing-context Open/Unverified block already carries every
capacity question this lens would reach (correlated re-attach churn at Tier 3, the mass §7.3
recovery storm through `resumeOnPod`, the exclusive-pool budget clamp, whether §4.6.1 orphan GC
reclaims a resume-path leaked slot). Reading it first is what kept this pass from re-deriving
four questions that were already asked and consciously left open for a human or a chaos-tier lens.


### [non-spec-recheck.1.review-reliability.1]

FACT: the delta since the last non-spec convergence is TINY. `diff -rq scratchpad/cp-snap/.../non-spec-r2 proposals/0081_...` shows `non-spec-changes.md` is BYTE-IDENTICAL to the non-spec-r2 snapshot; only `spec-changes.md` (two wording swaps: "the runtime's acknowledgement of it" -> "the moment the session reaches the runtime", and the `receiving_uploads -> running` trigger cross-reference dropped from SPEC-4) and `summary.md` (four 0080 impacts rows consolidated into one) moved. EVIDENCE: scratchpad/cp-snap/0081_.../non-spec-r2 vs proposals/0081_...

MISTAKE: the fix round that ran after the last non-spec sweep did NOT apply the one FILED-and-live reliability finding from that sweep. review-log.md:732 and :842 record "CODE-4's session-keyed credential release destroying a concurrent successor attempt's leases" as FILED and OPEN with a named remedy, and non-spec-changes.md:652 still carries the unmodified `b.releaseCredentials(req.SessionID)`. I re-filed it this round. Cost: a whole extra loop. A future fix pass should sweep `### Open` for entries marked FILED before declaring a lane converged.

FACT: the concurrency this finding needs is reachable and the proposal itself says so. summary.md:438-446 records "Concurrent `POST /v1/sessions/{id}/start` for one session is not serialized ... two concurrent calls both reach `BindReservedSlot` on the same pod with the same slot identifier", filed as a shipped defect the proposal does not stage. That is precisely the interleaving the epoch fence was added for, so anything else the compensating wrapper does session-keyed is exposed to it.

FACT: `CredentialAssigner` (pkg/gateway/podlifecycle/podsession/binder.go:319-333) exposes only `ReleaseSession(sessionID)`. `credassign.Service` DOES have a per-lease `Release(leaseID)` (credassign.go:380) and the `Releaser` interface at :53 declares it, but the binder's own consumer interface does not, so a lease-id-scoped release costs one interface method plus threading the minted ids out of `assignSlotCredentials` (which already builds a `leases` map, slotbinder.go:376-392). Bounded, not a redesign.

FACT (checked, do not re-derive): the per-connection epoch latch really is per-session in practice. `Binder.ClaimSlot`/`BindReservedSlot` call `b.DialAdapter` once per bind attempt (slotbinder.go:237, :459) and the connection is stored on `BindResult.Adapter` (:332); `podsession.Registry` is keyed by session (registry.go:14-28). No adapter connection is shared between two sessions, so the latch cannot cross-contaminate co-tenants.

FACT (checked): `slotCleanupBudget`'s divisor cannot be zero. `SlotBindRequest` is built at one site only (start.go:2541) and reached only under `match.MaxConcurrentSessions > 1`; `ResumeRequest.MaxConcurrentSessions` is normalized to a minimum of 1 by `maxConcurrentSessions()` (start.go:3353-3358, :4029). `CleanupTimeoutSeconds` IS carried on `SlotBindRequest` (start.go:2567) despite the neighbouring comment at :2524-2527 saying the concurrent request omits the cleanup parameters — the comment is about `CleanupCommands` semantics and is stale-looking; do not read it as an absence.

WATCHOUT: the §10.1.4 hold-termination path is safe for the new hold, but only because `onHoldTimeout`'s pass 2 has no early exit: it iterates every member of `deregisterStartedSessions()` and `terminateHeldSession` has a single straight-line body (pkg/adapter/holdstate.go:189-205, :229-269). If a future change adds a `continue`/`return` inside that loop, every skipped member's hold leaks for the life of the pod. EVIDENCE: pkg/adapter/holdstate.go:203-205.

UNVERIFIED: non-spec-changes.md:1354-1359 justifies clearing the client-side epoch latch on a `RECLAIMED` reclaim by saying a stale latch makes the adapter "answer `superseded`, removes nothing, and the slot is left standing". In the no-successor case the adapter answers ABSENT (the entry is gone), and in the successor case `superseded` is the CORRECT answer, so the stated harm does not occur; the clearing is harmless either way. Judged below the bar (the mechanism works; only its rationale is confused). A wording pass could fix it.

OPEN: nothing in the staging says whether `release()` (the hold releaser `reclaimSlotLocked` returns) acquires `s.mu` itself. CODE-1 registers `defer release()` while holding `s.mu` and executes it after `s.mu.Unlock()`; `terminateHeldSession` defers it with the lock never held. It must therefore take the lock itself, and `reclaimSlotLocked`'s stated "Callers hold `s.mu`" must not be read as applying to `release`. Left unfiled as an implementor's detail; whoever lands CODE-6 should say so in the doc comment.


### [non-spec-recheck.1.review-security.1]

DECISION: filed exactly one finding, CODE-4's session-keyed `releaseCredentials` — BECAUSE it
was already recorded as FILED-and-live in the review log (`review-log.md:732`, `:842`) and the
delta since the `spec-recheck-r5` snapshot did not touch it, so it is still unfixed in
`non-spec-changes.md:652`. ALTERNATIVES: I re-derived it independently under the security lens
rather than the reliability lens, and the §11.4 deny-list consequence below is new evidence the
earlier filing did not carry, so it is not a bare re-file.

FACT: the delta since the snapshot is ONE hunk. `diff -ru scratchpad/cp-snap/.../spec-recheck-r5
proposals/0081_...` touches only `summary.md` (the four 0080 impact rows consolidated into one)
and the review log. `non-spec-changes.md`, `spec-changes.md` and the checklist are byte-identical.
Do not spend a round re-diffing; the delta carries no mechanism.

FACT: §11.4's full-revoke fan-out enumerates leases through `LeasesBySession`, so a lease removed
from the store is invisible to the revoke and never reaches the §4.9 deny list that stops an
already-materialized proxy-mode lease token. EVIDENCE: cmd/lenny-gateway/user_revocation.go:147,
:176-183; pkg/gateway/credentials/credassign/credassign.go:406.

FACT: the proposal itself supplies the reachability premise for the credential finding. Its own
"Defects in the shipped tree that this proposal does not stage" states "Concurrent
`POST /v1/sessions/{id}/start` for one session is not serialized ... two concurrent calls both
reach `BindReservedSlot` on the same pod with the same slot identifier". EVIDENCE:
summary.md:437-448. A refutation that denies the concurrent successor has to overturn that row.

FACT: `AssignCredentialsResponse` is constructed in `pkg/adapter/slotcreds.go:51`
(`assignCredentialsSlot`), not in `pkg/adapter/credentials.go:65` where the RPC handler lives, so
SCHEMA-1's `bind_epoch` field and the "Files touched" entry naming `slotcreds.go` are correct.
I checked this as a suspected missing edit site and it is not one.

FACT: the five production `ensureSlotStateLocked`/`ensureSlotPaths` resolve sites CODE-6 names are
exactly the five that exist. `staging.go:134,:181,:337`, `slotsession.go:75`, `slotcreds.go:26`.
Every other hit is a test. Verified mechanically; do not re-derive.

WATCHOUT: `CODE-4`'s outcome switch has a `default: return false` arm, so an answer with
`err == nil`, `slot_reclaim == UNSPECIFIED` and `exited_cleanly == false` is recorded as NOT
leaked, where today's `leaked = err != nil || !cleanly` records it as leaked. I did NOT file it:
the first-party `Shutdown` sets the outcome on every return path, so only a non-conforming adapter
reaches it, and CONF-1 plus §15.4 own that surface. A future round that wants to file it needs a
reachable producer, not the hypothetical. EVIDENCE: non-spec-changes.md:596-620;
pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559.

WATCHOUT: `ExitedCleanly: closeErr == nil && (live || treeErr == nil)` discards the tree-removal
error whenever the session reached `running`, so a failed `removeSlotTree` on a live session still
leaves `/run/lenny/slots/{id}/credentials.json` on a pod that keeps serving and is reported clean.
Not a finding: the shipped handler already discards it (`_ = removeSlotTree(st)`,
pkg/adapter/session.go:271) and reports on `closeErr` alone (`:290`), so the staging is a strict
improvement on the non-live arm and a no-change on the live arm. Killed once here; kill it again
on the same evidence.

WATCHOUT: the §11.4 revoke's `Shutdown` for a bound-but-unstarted session stops running
`Runtime.Close` and `drainViaLifecycle` under CODE-1 (`started` replaces `bound`). This looks like
a revoke regression and is not: the runtime was never given that session, and the three `Close`
implementations are not session-scoped, so today's call tears down co-tenants. The credential file
is still reclaimed, and more of it than today, because the tree removal widens from `bound` to
`removed`. EVIDENCE: pkg/adapter/session.go:238-271 versus non-spec-changes.md:236-290.

UNVERIFIED: whether `credassign.Service` exposes any release-by-lease-id surface a fixer could use
for the suggested remedy. `releaseLocked(leaseID)` exists at credassign.go:414 but is unexported
and takes `s.mu`; a fixer needs to check whether an exported `ReleaseLeases([]string)` has to be
minted, and whether `UserCredentialAssigner` needs the same. Somebody landing the fix should
derive this before writing the deliverable text.


### [non-spec-recheck.1.review-test-coverage.1]

FACT: the delta this recheck was fired for is TINY and touches no test obligation. `diff -ru`
against `scratchpad/cp-snap/.../non-spec-r2` shows `non-spec-changes.md` BYTE-IDENTICAL to the
last non-spec convergence; only `summary.md` (open decision 15 deleted with its resolution
written into the preamble; the four 0080 impact rows consolidated into one) and
`spec-changes.md` (three sites reworded from "the runtime's acknowledgement of it" to "the
moment the session reaches the runtime") moved. EVIDENCE: summary.md:334-361, :656-658;
spec-changes.md:18-21, :87-93, :322, :660.

FACT: `scratchpad/cp-snap/.../non-spec-recheck-r1-start` is byte-identical to the live
proposal outside the review logs, so it is useless as a delta baseline; use `non-spec-r2` for
the non-spec lane and `spec-recheck-r5` for the spec lane.

FILED: CONF-1's tier-10 battery covers only ONE of the TWO contracts SPEC-5 publishes in
§15.4. §15.4 gains a **Bind epoch contract:** block AND a **Slot-identifier reclaim hold:**
block (spec-changes.md:713), the latter carrying two explicit non-conformance statements
("admits such a request while the cleanup is running does not conform"; "refuses one with a
permanent status ... does not conform") and a named wire code (`ABORTED`). CONF-1's four
properties are all epoch properties (non-spec-changes.md:1093-1104), the checklist's S14 line
names the same four (implementation-checklist.md:32), and the summary's own rationale says a
battery is "the deliverable that makes that contract checkable" (summary.md:686-688). The hold
is pinned at tier 1 and tier 7a against the first-party adapter only.
EVIDENCE: spec-changes.md:713; non-spec-changes.md:1089-1093, :1807-1810; summary.md:680,:686.

WATCHOUT: an earlier round refuted a §15.4-status-code finding partly on "CONF-1 having
nothing extra to assert about a code the spec does not yet name". That premise is STALE — the
current §15.4 hold block names `ABORTED` outright (spec-changes.md:713). Do not reuse that
refutation against the hold-battery gap.

WATCHOUT: do NOT re-file "`slotCleanupBudget` has no listed test". A prior round refuted it and
the review log records the refutation plus a standing OPEN that supersedes it
(review-log.md:787).

FACT: tier 10 is a workable home for a hold case. `tests/tier10_conformance/recycle_scrub_conformance_test.go`
drives the exported `adapter.Server` directly with a fake runtime rather than a runtime binary
over JSONL, so parking `Runtime.Close` and asserting an `ABORTED` refusal is the same fixture
pattern CONF-1 already inherits. EVIDENCE: tests/tier10_conformance/recycle_scrub_conformance_test.go:12-31.

FACT: the rest of the Testing section survives a per-deliverable sweep. Every staged behavior
I could name has a listed test at a tier the rule maps: CODE-6's hold and epoch (tier 1 + 3 +
7a + 9), CODE-1's two teardowns and three outcomes (tier 1 + 3 + 9), CODE-2's start
confirmation in both orderings (tier 1 + 7a), CODE-4's six outcome arms including the
`SUPERSEDED`-clean arm the pre-amendment `err != nil || !cleanly` got backwards (tier 1 + 4),
CODE-5's `ExcludePods` (tier 1 + 2 + 4) and its `codes.Aborted` arm of `isTransientPodClaimError`
(the only production caller is `holdOrFailOnResumeError`, pkg/gateway/sessionserver/start.go:3610,
and the listed `TestHoldOrFailOnResumeErrorReclaimHold_spec_7_3` covers it), SCHEMA-1's shipped
closed-field-set gate (tier 3), DOCS-1/DOCS-2 (tier 11). No SDK mirrors the adapter proto
(`sdks/` has only `client` and `runtime`, neither referencing `adapter/v1`), so no SDK tier is
missed.


### [non-spec-recheck.2.fix-G1.1]

DECISION: added `pkg/adapter/exportpaths_test.go` and `pkg/adapter/one_session_only_test.go` to the `Tests:` bullet of "Files touched on application (non-spec)" and one clause in CODE-6 beside the `ensureSlotPaths` widening prose saying their call sites are retargeted to the widened returns in S9, discarding the epoch — BECAUSE both files are `package adapter` and call the two widened functions, so package `adapter` would stop compiling at S9 — ALTERNATIVES: enumerating each call site with file:line (line numbers drift and the proposal only cites lines where a call needs more than a discard); leaving it to the implementor (an omitted file is an unstaged site by this proposal's own convention); spelling the widened call form literally (CODE-6 deliberately leaves the epoch's position in `claimSessionSlot`'s return unsettled).
FACT: the exhaustive sweep for this class is `grep -rn "ensureSlotPaths\|claimSessionSlot\|resolvePrepareStagingDir\|noteRuntimeStarted\|claimPodMCPStart" --include=*_test.go .`, which returns exactly seven files: `adapterevents_test.go`, `export_test.go`, `exportpaths_test.go`, `one_session_only_test.go`, `podmcp_arming_internal_test.go`, `slotsession_test.go`, `usage_test.go`. After this fix all seven are listed and the widened-function test-caller surface is complete. EVIDENCE: pkg/adapter/exportpaths_test.go:25,221,225; pkg/adapter/one_session_only_test.go:91,94,125,159,194,209,212
CORRECTS [non-spec-recheck.2 finding "The edit list omits the two adapter test files…"]: the finding and its design both say `one_session_only_test.go` holds nine `claimSessionSlot` call sites. It holds seven (lines 91, 94, 125, 159, 194, 209, 212). The staged clause carries no count, both because the count was wrong and because the loop bans counts in the change files.
WATCHOUT: `pkg/adapter/socketruntime_test.go` is in the `Tests:` bullet but calls none of the five widened or changed adapter functions; it is there for another reason. Do not treat the `Tests:` bullet as the widened-caller set. EVIDENCE: proposals/0081_…non-spec-changes.md, the `Tests:` bullet of "Files touched on application (non-spec)"


### [non-spec-recheck.2.fix-design-G1.1]

DECISION: Treat G1's single finding as trivial — add `pkg/adapter/exportpaths_test.go` and `pkg/adapter/one_session_only_test.go` to the `Tests:` bullet of "Files touched on application (non-spec)" and add one clause in CODE-6 (beside the `ensureSlotPaths` widening prose, non-spec-changes.md:1029-1031) saying those call sites are retargeted to the widened returns in S9 — BECAUSE the finding's tree evidence checks out exactly and nothing else in the proposal counts or closes CODE-6's test-caller surface — ALTERNATIVES: (a) enumerate each of the twelve call sites with line numbers, rejected as the proposal already uses the coarse form elsewhere and line numbers drift; (b) leave it to the implementor as a mechanical compile fix, rejected because the proposal's own edit lists are what S9 is executed from.

FACT: The widened-function test-caller surface is exactly seven files, and the exhaustive sweep is one grep. `grep -rn "ensureSlotPaths\|claimSessionSlot\|resolvePrepareStagingDir\|noteRuntimeStarted\|claimPodMCPStart" --include=*_test.go .` returns only pkg/adapter/{adapterevents,export,exportpaths,one_session_only,podmcp_arming_internal,slotsession,usage}_test.go. Five are already listed; the two the finding names are the whole gap. No caller outside package adapter exists (all five identifiers are unexported). EVIDENCE: pkg/adapter/exportpaths_test.go:25,221,225; pkg/adapter/one_session_only_test.go:91,94,125,159,194,209,212.

WATCHOUT: CODE-6 never fixes the ARGUMENT ORDER of the widened `claimSessionSlot` return. Current signature is `(fresh, startMCP bool, err error)` (pkg/adapter/slotsession.go:52) and the staged text only says the two claim functions "report the epoch" (non-spec-changes.md:365, 2052). Do not have the added clause spell a four-value form such as `_, _, epoch, err`; write it as "retargeted to the widened returns" so it does not contradict whatever position CODE-6 settles on. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:365-367.

UNVERIFIED: whether `exportpaths_test.go`'s three `ensureSlotPaths("sess-N")` calls need a bound registry entry once the hold/epoch land (the epoch minting rule is entry-creating, so they probably stay green with a discarded epoch). A later code-lane round should confirm rather than assume.


### [non-spec-recheck.2.review-applicability.1]

FACT: The r2 delta (diff `non-spec-recheck-r1-prefix` -> current) is exactly three things, and the summary was NOT touched: (1) `resolvePrepareStagingDir` widens to `(string, int64, error)` so `PrepareWorkspace` can report an epoch, plus the staging.go files-touched line and the S9 checklist line; (2) the `ExcludePods` placement predicate narrows from `sbe.Leaked || relErr != nil` to `sbe.Leaked` alone, with a new tier-1 case "A failed reservation release does not move the retry"; (3) the CODE-6 tier-1 bullets gain **S9./S10./S11.** step prefixes and the S9/S10/S11 checklist lines gain matching tier-1 clauses. EVIDENCE: non-spec-changes.md:1030-1039, :776-785, :836-841, :1552-1557, :1326-1332; implementation-checklist.md S9/S10/S11.

FACT: Every claim the delta makes about the tree checks out. `resolvePrepareStagingDir` is the only route `PrepareWorkspace` has to the entry (pkg/adapter/staging.go:78, :133-135 -> `ensureSlotPaths`). Both gateway callers guarantee >=1 upload frame: `stageWorkspace` gates on `if len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323) and the mid-session path rejects an empty file list before it calls (pkg/gateway/sessionserver/upload_to_session.go:183-186), so no `PrepareWorkspaceResponse` can report zero. `applySlotRetryPolicy` retries regardless of `relErr` (pkg/gateway/sessionserver/start.go:2834-2882) and at mCS=4 one leak does not cross `ceil(4/2)`, so the new tier-1 case is writable.

FACT: All nine staged proto field numbers are free on their messages and the enum obeys buf's prefix rule. Verified against schemas/lenny-adapter.proto: `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved so 7 is next; `ShutdownResponse` holds 1,2 so 3; `PrepareWorkspaceResponse` 1,2 so 3; `ResumeResponse` 1,2,3 so 4; `AssignCredentialsResponse` is `{}` so 1; `FinalizeWorkspaceResponse`/`ConfigureWorkspaceResponse`/`RunSetupResponse`/`StartSessionResponse` each hold only field 1 so 2. Do not re-derive this table.

FACT: The two staged claim-register rows satisfy the tier-0 validator's schema. `tests/tier0_static/claim_register_test.go:29-45` holds WIRED rows to "names a surface, carrying a file path or a symbol rather than a bare line number", and holds only the three per-slot CREDENTIAL rows to the `adapterClientReachableRPCs` reachability gate; rows outside that set carry the schema rules alone. `#2851-gateway-to-pod` already resolves (three existing rows use it, tests/claim-map.json:22,:30,:37).

FACT: The two tier-11 gate anchors all resolve as staged. docs/reference/adapter-contract.md: `Shutdown` row at :75, `DemoteSDK` at :64, `**Scrub responsibilities.**` at :84. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` reads the row via `lineContaining(page, "| \`Shutdown\` |")` and asserts exactly the four substrings the proposal names (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:298-307), all of which survive the staged row. docs/reference/state-machines.md's per-slot table is From|To|Trigger at :232-237 and the `receiving_uploads`->`running` insertion point at :235 is unique.

WATCHOUT: `releaseSessionSlot` calls NO `Runtime.Close`. Its whole body is `deregisterSlot` -> `removeSlotTree` -> `cancelPodMCPIfRuntimeIdle` (pkg/adapter/slotsession.go:214-219). Only `Shutdown` (pkg/adapter/session.go:263) and `terminateHeldSession` (pkg/adapter/holdstate.go:243) close the runtime. The r2 fix stage, splitting the tier-1 hold cases off `Shutdown` onto "the two sites CODE-6 itself routes", wrote two bullets that tell the implementor to park or panic `Runtime.Close` at `releaseSessionSlot` (non-spec-changes.md:1347-1351, :1367-1370). Only the §10.1.4 half of each disjunction is writable. Filed. EVIDENCE: pkg/adapter/slotsession.go:214-219.

FACT: `terminateHeldSession` IS synchronously drivable from a test, so the §10.1.4 half of those bullets works: `holdstate_test.go:158` already calls `s.onHoldTimeout()` directly rather than waiting on the `holdAfter` timer, and pass 2 runs in the caller's goroutine (pkg/adapter/holdstate.go:198-204). A panic there is recoverable at the test boundary.

FACT: `removeSlotTree` is `slotlayout.RemoveTree(st.paths)` with no injectable seam (pkg/adapter/slot.go:210-212), so the `releaseSessionSlot` ROW of the "every deregister-then-destroy site takes the hold" table (non-spec-changes.md:1353-1360) has no parkable destructive step either. That half is PRE-EXISTING (the same sentence stood pre-delta over three rows) and I did not file it separately; a future round wanting it must argue past the refutation precedent that test-plan fixture detail is polish.

FACT: Checklist audit is clean and I re-ran it in full. Fourteen steps, one lane each, spec block S1-S5 leads, deliverable-to-step is a bijection over SPEC-1..5 / SCHEMA-1 / CODE-1..6 / DOCS-1,2 / CONF-1, every `Depends on` names an earlier existing step, no box is ticked. The two tier-list gaps that look wrong are answered in the Testing section by name: S9 omits tier 9 because the tier-9 file is created at S10 ("in the same step, S10", non-spec-changes.md:1821-1822), and S5 omits 11 and 1 with its reason on its own line.

USEFUL [Standing context Trap 239]: saved me from filing S9's `Depends on: S1, S8` not closing over S4. Still holds after the delta.
USEFUL [Standing context Trap 238]: the stale "Between S4 and S6" at non-spec-changes.md:1668 is still there after the delta (SPEC-4 is S5). Confirmed not a finding; do not re-file.


### [non-spec-recheck.2.review-fresh.1]

WATCHOUT: `awk '/^message X \{/,/^\}/'` over `schemas/lenny-adapter.proto` silently bleeds into the NEXT message when the target is declared on one line as `message X {}`. That is exactly how `AssignCredentialsResponse` (a genuinely empty message at schemas/lenny-adapter.proto:1033) appears to already hold fields 1,2,3,5 — they belong to `RotateCredentialsRequest`. Use a brace-depth extractor. I nearly filed a false "field 1 is taken" finding on the proposal's `AssignCredentialsResponse.bind_epoch = 1`. — EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: every field number in SCHEMA-1's nine-field table is free, verified with a brace-depth parser: PrepareWorkspaceResponse holds 1,2 (→3 free); FinalizeWorkspaceResponse 1 (→2); ResumeResponse 1,2,3 (→4); ConfigureWorkspaceResponse 1 (→2); RunSetupResponse 1 (→2); AssignCredentialsResponse empty (→1); StartSessionResponse 1 (→2); ShutdownRequest 1,2,3,5,6 with 4 reserved (→7); ShutdownResponse 1,2 (→3). — EVIDENCE: schemas/lenny-adapter.proto:699,761,1433,1690,869,1033,958,1609,1665

FACT: the delta this round (vs `scratchpad/cp-snap/.../non-spec-recheck-r1-prefix`) is exactly four things, all in non-spec-changes.md plus three checklist lines: (1) the `ExcludePods` append predicate narrowed from `sbe.Leaked || relErr != nil` to `sbe.Leaked` alone, with the two-predicate rationale and a new tier-1 case at :1553; (2) `resolvePrepareStagingDir` widened to carry the epoch, with the "every gateway call carries ≥1 upload frame" argument; (3) the tier-1 bindepoch cases split across S9/S10/S11 with **S10.**/**S11.** prefixes; (4) staging.go's files-touched bullet gained `resolvePrepareStagingDir` and `PrepareWorkspace`. The `non-spec-recheck-r2` snapshot directory is byte-identical to the live proposal, so `diff` against it yields nothing — use `non-spec-recheck-r1-prefix`.

FACT: delta (2) checks out. Both production `PrepareWorkspace` callers guarantee at least one frame: `stageWorkspace` guards on `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323-1330) and `parseUploadToSession` rejects an empty `req.Files` with a 400 before `handleUploadToSession` calls it (pkg/gateway/sessionserver/upload_to_session.go:128; parseUploadToSession's `if len(req.Files) == 0` guard). The proposal's justification names only `stageWorkspace`, but the conclusion holds for both.

FACT: delta (1) checks out and is a real fix. `applySlotRetryPolicy` retries regardless of the `ReleaseSlotReservation` outcome (pkg/gateway/sessionserver/start.go:2834-2884), so the new tier-1 case at non-spec-changes.md:1553-1557 is reachable, and `sbe.Leaked` (set only from `compensateFailedSlotBind`, non-spec-changes.md:640) is exactly §7.1's "reclaim did not complete" predicate, while `relErr` is not.

FACT: the "Files touched on application" test list was built by enumerating `noteRuntimeStarted`'s callers — all seven test call sites are named at non-spec-changes.md:2087-2096 and re-enumerated at :470-478. It was NOT rebuilt for the other two widened functions. `ensureSlotPaths` has three test callers in `pkg/adapter/exportpaths_test.go` (:25,:221,:225) and `claimSessionSlot` has nine in `pkg/adapter/one_session_only_test.go` (:91,:94,:125,:159,:194,:209,:212 and two more); neither file is in the list. Both are `package adapter`. FILED.

FACT: `releaseSessionSlot` reaches no `Runtime.Close` — its body is `deregisterSlot` → `removeSlotTree` → `cancelPodMCPIfRuntimeIdle` (pkg/adapter/slotsession.go:214-220), and `cancelPodMCPIfRuntimeIdle` only runs stored cancel funcs. Only the §10.1.4 hold termination (pkg/adapter/holdstate.go:243) and `Shutdown` call `Runtime.Close`. The fix round's rewrite of the tier-1 hold bullets names `releaseSessionSlot` as a `Runtime.Close` park site. FILED.

FACT: S-2's covered handler files are exactly `session.go, lifecycle.go, checkpoint.go, coordination.go, credentials.go, slotcreds.go, attach.go, sdkwarm.go` plus the two renamed channel files (gateway-runtime-comms-remediation.md:1883-1890). The proposal's "three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`)" is correct: `staging.go`, `slot.go`, `slotsession.go`, `resume.go`, `holdstate.go`, `runtimegeneration.go` are not covered.

FACT: verified and NOT findings — the five resolve sites and their wraps/span categories all match non-spec-changes.md:1044-1052 exactly (pkg/adapter/staging.go:80,134-138,181-186,337-342; slotsession.go:75-79; slotcreds.go:26-29); `materializeSlot` has exactly five `cl.Close()` calls (pkg/gateway/podlifecycle/podsession/slotbinder.go:265+); `TestShutdownMessagePostRemovalDescriptor_spec_4_1`'s `wantReq` and `assertFieldSet` are as cited (tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-237,:259-267); `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`'s four substrings all survive DOCS-2's staged row (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307); `docs/reference/adapter-contract.md:64` and `:75` are the `DemoteSDK` and `Shutdown` rows DOCS-2 cites.

DEFERRED [non-spec-changes.md:861]: "The `slotBinder` interface is unchanged … so every implementing type and both test fakes compile as they stand" is false read literally — CODE-4 widens `ReleaseSlotReservation` to `(ctx, sandboxName, slotID string, leaked bool)` (:676) and the table at :692 stages "`start.go` `slotBinder` interface declaration | signature only". The sentence is contextually scoped to the `ExcludePods`/by-value question, so I judged it below the bar; what is true is that the interface and both fakes DO change, at S12, before the S13 `ExcludePods` work. Worth one clause ("unchanged by this field") if anyone edits that bullet.

DEFERRED [summary.md:687]: CODE-6's file enumeration omits `pkg/adapter/session.go` and `pkg/adapter/resume.go`, yet CODE-6 owns "the seven handlers report the entry's epoch on their responses", two of which are `StartSessionResponse` (session.go) and `ResumeResponse` (resume.go). The non-spec files-touched bullet for `session.go` (:2045-2046) likewise omits the epoch on the `StartSession` response, while resume.go's bullet (:2058) names it. Both files are in the overall list, so no edit site is lost; the per-deliverable attribution is what is incomplete.

UNVERIFIED: §15.4's conformance clause "does not conform when it … reports zero on a bind-sequence response" is unsatisfiable for a `PrepareWorkspace` call that carries zero frames, since the adapter never resolves an entry and has no epoch to report. The non-spec staging answers this only for the two gateway callers, both of which do send ≥1 frame. Nobody has checked whether a conformance harness or third-party caller can open an upload-free `PrepareWorkspace` stream. A spec-lane reviewer should decide whether §15.4 needs "a call that resolves no entry" carved out.


### [non-spec-recheck.2.review-mechanism.2]

DECISION: returned an empty findings list for the end-to-end-mechanism lens on the non-spec
staging — BECAUSE every flow I traced (epoch mint → response report → client latch →
compensating Shutdown → outcome map → leaked disposition → ExcludePods placement → retry;
and hold insert → refusal at ensureSlotStateLocked → Aborted → two gateway classifiers)
resolves against the tree, and the four candidates I derived each collapsed on inspection —
ALTERNATIVES: I considered filing (a) the UNSPECIFIED outcome falling into CODE-4's
`default: return false` (fail-open, but unreachable from the first-party adapter, which always
sets ABSENT or RECLAIMED, and the loop has already refuted two "hypothetical third-party
adapter" findings); (b) the §4.7.1 caller rule enumerating "DemoteSDK, and a Shutdown answering
reclaimed" while CODE-6 clears the latch only on DemoteSDK and on ShutdownReclaim→RECLAIMED,
so an ordinary `Client.Shutdown` that removes the entry leaves the latch standing — no
reachable harm, because any later bind-sequence response on that connection re-latches before
any compensation and a stale latch against a removed entry answers ABSENT; (c) the "seven
residues survive" count in summary.md's Decisions bullet against the 4+2 it then enumerates —
the loop already refuted a stale-count finding as immaterial; (d) CODE-4's justification that
`Binder.Resume`'s unfenced compensation is safe "because no earlier RPC of that attempt created
an entry" — the Resume RPC itself creates the entry via claimSessionSlot (resume.go:50), so the
justification is loose, but the loop already refuted the identical "precedes any successor"
wording objection on the sibling bullet as rationale polish.

FACT: `AssignCredentialsResponse` really is an empty message, declared on one line as
`message AssignCredentialsResponse {}` — EVIDENCE: schemas/lenny-adapter.proto:1033. WATCHOUT
for the verification method: an `awk '/^message X \{/,/^\}/'` extraction silently runs past a
one-line message and reports the NEXT message's fields, which made me briefly believe field 1
was taken by `SessionId session_id = 1`. Use a brace-depth scan. I re-verified all nine
SCHEMA-1 field numbers that way and every one is free:
ShutdownRequest 1,2,3,[4 reserved],5,6 → 7 free (:1609); ShutdownResponse 1,2 → 3 free (:1665);
PrepareWorkspaceResponse 1,2 → 3 (:699); FinalizeWorkspaceResponse 1 → 2 (:761);
ResumeResponse 1,2,3 → 4 (:1433); ConfigureWorkspaceResponse 1 → 2 (:1690);
RunSetupResponse 1 → 2 (:869); StartSessionResponse 1 → 2 (:958);
AssignCredentialsResponse empty → 1 (:1033).

FACT: `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is the only closed field-set gate over
the messages SCHEMA-1 opens, and the proposal's account of it is exact: `wantReq` at
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:211-217, the
ShutdownResponse `assertFieldSet` at :233-237, and `assertFieldSet`'s unexpected-field arm at
:264-267. The other file-wide descriptor suite,
tests/tier3_contract/adapter_session_address/session_address_wire_test.go, only checks
`slot_id` absence, `session_id` presence and reserved ranges (:110-176), so additive response
fields do not move it.

FACT: `status.Code(err)` in grpc v1.80.0 DOES walk a wrapper chain — it calls `Convert` →
`FromError`, which falls back to `errors.As` — EVIDENCE:
/home/ec2-user/go/pkg/mod/google.golang.org/grpc@v1.80.0/status/status.go:112-113,:139-146.
CODE-5's `isTransientPodClaimError` arm therefore fires through the `*SlotBindError` that
CODE-4 makes `Binder.Resume` return, as the proposal claims.

FACT: the hold refusal's blast radius is exactly the bind-sequence and §7.4 upload entry
points, because `ensureSlotStateLocked` has exactly three callers in production:
`ensureSlotPaths` (pkg/adapter/slot.go:143, itself called only from staging.go:134/:181/:337),
`claimSessionSlotUnderLock` (slotsession.go:75) and `assignCredentialsSlot` (slotcreds.go:26).
Every other session-scoped handler resolves through `slotStateLocked`/`boundSlotState` and is
untouched by the hold.

FACT: every `PrepareWorkspace` call the gateway makes carries at least one upload frame, so the
"response always reports a non-zero epoch" rule has no hole on the §7.4 path either:
`parseUploadToSession` rejects an empty file list outright
(pkg/gateway/sessionserver/upload_to_session.go:183-186) and `sendUpload` always sends at least
one frame per upload (pkg/gateway/runtime/adapterclient/client.go:267-286).

FACT: both request types the compensation reads carry the budget inputs —
`podsession.SlotBindRequest.MaxConcurrentSessions`/`CleanupTimeoutSeconds`
(pkg/gateway/podlifecycle/podsession/slotbinder.go:39,:108) and
`podsession.ResumeRequest`'s (binder.go:645,:667) — so `slotCleanupBudget`'s scalar signature
serves both callers as staged.

FACT: `bindConcurrentSlot`'s `runWithQueue` closure captures the `slotReq` PARAMETER
(pkg/gateway/sessionserver/start.go:2594,:2606-2609), so CODE-5's `&slotReq` is a stable address
across a queue re-entry and the `ExcludePods` append really does survive it. Confirmed
`applySlotRetryPolicy` is by-value today at :2808 and `SlotBindError.Reason()`'s
`InvalidArgument → SlotReasonWorkspaceValidation → NonRetryable()` chain at
pkg/gateway/podlifecycle/podsession/slotfailure.go:41-48,:85-101, which is the trap CODE-6's
`slotResolveError` exists for.

UNVERIFIED: whether `codes.Aborted` has any other producer reachable through
`holdOrFailOnResumeError` that should stay terminal. I checked the arms
`isTransientPodClaimError` carries today (start.go:3648-3681) and none is an Aborted status, but
I did not sweep every error the resume path can surface. A security or reliability lens should
close it.


### [non-spec-recheck.3.review-fresh.1]

DECISION: returned an EMPTY findings list — BECAUSE every load-bearing claim I could reach
resolved against the tree, and the two candidate defects I derived both fall inside families
this loop has already refuted — ALTERNATIVES: (a) filing that CODE-6's new sentence enumerates
only 2 of the 5 test files whose `ensureSlotPaths`/`claimSessionSlot` call sites break when those
signatures widen (rejected: every omitted file is already in "Files touched on application", and
the identical shape was refuted as edit-list granularity in the `deregisterSlot` finding);
(b) filing that the files-touched entry for `pkg/adapter/session.go` omits `StartSessionResponse.bind_epoch`
while every other of the seven response-epoch sites is named in its own file's entry (rejected:
same granularity class, and CODE-6's "seven handlers report the entry's epoch on their responses"
covers it); (c) filing the summary's "Seven residues survive" against the four-plus-two it then
enumerates (rejected: same category as the already-refuted "Four statements are needed" count).

FACT: THE r3 SNAPSHOT IS USELESS. `scratchpad/cp-snap/0081_.../non-spec-recheck-r3` and
`-r3-start` are both byte-identical to the live proposal. The real delta for this round is
`non-spec-recheck-r2-prefix` → live, and it is exactly TWO hunks in non-spec-changes.md: the
sentence at :1031-1033 naming `exportpaths_test.go` and `one_session_only_test.go` as retarget
sites, and those two files added to the test list at :2084. Diff against `-r2-prefix`.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/

FACT: the proto field-number sweep is CLEAN and re-verifiable in one script, but a naive
`awk '/^message X \{/,/^\}/'` LIES on this file, because `AssignCredentialsResponse {}` and
several siblings are declared on one line, so the range runs on into the NEXT message and
reports that message's fields. That is how a reader concludes `AssignCredentialsResponse`
already holds field 1. It does not; it is empty at schemas/lenny-adapter.proto:1033. Verified
free: ShutdownRequest 7 (1,2,3,R4,5,6 held), ShutdownResponse 3, PrepareWorkspaceResponse 3,
FinalizeWorkspaceResponse 2, ResumeResponse 4, ConfigureWorkspaceResponse 2, RunSetupResponse 2,
AssignCredentialsResponse 1, StartSessionResponse 2. Use a one-line-message-aware parser.
EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1033,1433,1609,1665,1690

FACT: the claim register generator does NOT scan the proto. Rows come from spec status tables,
the `EXPLICIT` list, and `GENERATION_FENCE`; the tier-0 gate only re-runs the generator and
compares bytes, and the validator checks status/surface/anchor-resolution/deferral-resolution.
So SCHEMA-1's two `WIRED` `EXPLICIT` rows need no spec status table and no `deferral_id`.
EVIDENCE: scripts/seed-claim-register.py:168-169,:378-388; tests/tier0_static/claim_register_test.go:23-63

FACT: `tests/claim-map.json` today is exactly 76 rows, 32 WIRED / 24 UNWIRED / 20 ABSENT, which
is what the summary's §1.12 impact row says. No drift.

FACT: the §4.7.1 "every bind-sequence response reports a non-zero epoch" rule survives the
§7.4 mid-session upload path independently of `stageWorkspace`: `parseUploadToSession` refuses a
request with no file ("at least one file is required"), so `handleUploadToSession`'s
`PrepareWorkspace` always carries at least one frame and the handler always resolves the entry.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:184-185,:128

FACT: exactly three production sites call `ensureSlotStateLocked` (`claimSessionSlotUnderLock`
slotsession.go:75, `ensureSlotPaths` slot.go:143, `assignCredentialsSlot` slotcreds.go:26) and
exactly three call `removeSlotTree` (slotsession.go:217 `releaseSessionSlot`, session.go:271
`Shutdown`, holdstate.go:254 `terminateHeldSession`). CODE-6's "three entry points" and "three
deregister-then-destroy sites" are both exact against the tree as of this round.

USEFUL [Standing context / anchor sweep]: the Settled entry saying the spec anchor sweep is done
and should not be re-run saved me from re-deriving sixteen sites; I spot-checked the
docs/reference/adapter-contract.md anchors (:10, :64, :75) and the tier-11 gate instead, and both
resolved.

WATCHOUT: the `// spec:` line in CODE-4's mapping has no arm for
`SLOT_RECLAIM_OUTCOME_UNSPECIFIED`; a zero outcome with `exited_cleanly: false` falls to
`default: return false` and is accounted NOT leaked. It is unreachable against the first-party
adapter (CODE-1 always populates the field) and the adapter is never third-party, so it does not
clear the bar — but if a future change makes the adapter pluggable, that default is fail-open.
EVIDENCE: non-spec-changes.md:561-582


### [non-spec-recheck.4.fix-G1.1]

DECISION: The tier-4 datastore-crossing case's trigger is now an UNANSWERED compensating `Shutdown` rather than one "the adapter refuses" — BECAUSE after the amendment an adapter refusal is the `SUPERSEDED`/`ABSENT` answer, which CODE-4 maps to `leaked=false` (non-spec-changes.md:568-575), the opposite of both assertions the paragraph exists for — ALTERNATIVES: the RECLAIMED-without-clean-exit arm (needs a second fixture seam and reads badly beside CODE-5's own justification, which is stated over the unanswered case at :958-960); deleting the paragraph (it is the only coverage of the withheld Redis decrement with the `SandboxClaim` surviving at `bound`, and of the `ExcludePods` re-bind); splitting it in two (doubles envtest+miniredis setup for no extra coverage).

DECISION: The category assertion for `slotResolveCategory` was added to the existing five-resolve-site bullet, asserted through the handler's recorded span rather than over the helper in isolation — BECAUSE a helper-only unit assertion stays green when an implementor routes the error through `slotResolveError` and leaves the three literal `tracing.CategorizeError(err, tracing.CategoryPermanent)` stamps in place, which is exactly the ship-it-broken path — ALTERNATIVES: a direct `slotResolveCategory(sentinel)` unit (pins the helper and nothing else); a separate new bullet (duplicates the same table-driven fixture); dropping `slotResolveCategory` and deriving the category from the wrapped status code at the three sites (smaller, but reopens CODE-6's stated surface and the five-site table for no coverage gain).

FACT: `tracing.Category` does not exist. The type is `tracing.ErrorCategory` (`pkg/observability/tracing/tracing.go:88-89`), and the proposal's CODE-6 signature said `tracing.Category` — EVIDENCE: pkg/observability/tracing/tracing.go:88; corrected at non-spec-changes.md:1012.

FACT: The category is readable from an in-package tier-1 test with no new fixture. `tracing.RecordError` attaches it as the `error.category` span attribute, and `pkg/adapter/tracing_internal_test.go` (package `adapter`, same package as the new `bindepoch_test.go`) already ships `installInternalSpanRecorder` and `endedSpanNamed` — EVIDENCE: pkg/observability/tracing/tracing.go:107,:202; pkg/adapter/tracing_internal_test.go:23,:33.

FACT: The tier-4 unanswered-reclaim seam needs no new harness. `recycleAdapterDialer` builds the client through `adapterclient.Dial`, which is variadic over `grpc.DialOption`, so a unary client interceptor keyed on the `Shutdown` RPC's `slot_bind_failed` reason is a local addition — EVIDENCE: tests/tier4_integration/recycle_scrub_path_test.go:206; pkg/gateway/runtime/adapterclient/client.go:48; `ShutdownReclaim` rides the same `Shutdown` RPC through `Client.shutdown` at client.go:813.

WATCHOUT: After this round, `leaked=true` in this proposal means only a reclaim that did not complete (no answer at all, or `RECLAIMED` without a clean exit), and the word "refuse" in `## Testing` denotes only the reclaim-hold refusal and the epoch refusal. A future edit that writes "the adapter refuses" as a leak trigger is re-introducing the pre-amendment `err != nil || !cleanly` reading the amendment deleted — EVIDENCE: non-spec-changes.md:568-575.

MISTAKE: The stale trigger was pre-amendment residue rather than a fresh error. The sentence is byte-identical at the pre-amendment converged commit 2811d55fe, where no `SUPERSEDED` outcome existed and "refuses" could only mean an errored RPC. The hand amendment did not sweep the Testing section's vocabulary.


### [non-spec-recheck.4.fix-G2.1]

FACT: `tests/spec-map.json` is not the only register a new test file must enter. `tests/tier0_static/spec_map_slot_address_registration_test.go` holds a hand-written `slotAddressCaseFiles` inventory and derives two completeness rules from the tree, so a test file is mandatory in that inventory when its name matches `slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` OR when its body matches `slotSurfaceCallRE` = `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`. `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` fails tier 0 for any such file the list omits. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1173 (name rule), :1156 (call rule), :1191 (walk roots cmd, migrations, pkg, scripts, sdks, tests), :1212-1229 (derivedInventoryCaseFiles), :1108-1131 (the gate)

WATCHOUT: the inventory edit must land in the SAME commit as the file it names. The gate reads the tree both ways: an inventory entry for a file that does not exist yet dies in `repoFileBytes`'s `t.Fatalf` (:1269-1276), and an existing matching file with no entry fails the omission check. Splitting the two across commits is red in both directions. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1269

WATCHOUT: entering a file in `slotAddressCaseFiles` pulls two further gates onto it. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:969-988) demands a `tests/spec-map.json` credit under EVERY section the file's own `// spec:` annotation names, and `TestAddressCaseNamesAgreeWithTheirOwnCitations` (:1025-1038) demands a `_spec_X_Y` name suffix agree with that annotation. So widening a new case's annotation beyond the section its spec-map entry and its name state is an immediate tier-0 failure. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1025-1038

DECISION: pinned the new tier-7a case `TestSlotIdentifierReclaimHoldRefusesABindUntilTheCleanupReturns_spec_5_2` to `// spec: §5.2` alone in the proposal text — BECAUSE its name suffix and its staged spec-map entry both state 5.2, and the two gates above then pass without staging further credits — ALTERNATIVES: leaving the annotation unstated (the implementor guesses and can go red), or widening it to §4.7/§7.4, which the subsection also discusses and which would demand spec-map credits this proposal does not stage.

FACT: `pkg/gateway/sessionserver/start_test.go`, which CODE-5 edits, is absent from `slotAddressCaseFiles` and today contains zero matches of `slotSurfaceCallRE`. It is the plausible place a `BindReservedSlot(` literal first appears, which would make the file mandatory in the inventory. That is why the register rule was stated once, generally, beside the `tests/spec-map.json` bullet rather than per file. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:290 (nearest neighbour in the list; start_test.go itself is not there)

FACT: the gate is currently exhaustive. Every tracked `_test.go` under the six walk roots that matches either derived rule is already a literal in `slotAddressCaseFiles`, so it is live rather than dormant and any omission this proposal introduces goes red on the first tier-0 run.

UNVERIFIED: whether any other test file this proposal EDITS (rather than creates) gains one of the six slot-surface call literals. Only `pkg/gateway/sessionserver/start_test.go` was checked for present-day matches. A later round checking the staged test bodies should re-run the call regexp against every file in the `Tests:` list of `## Files touched on application (non-spec)`.


### [non-spec-recheck.4.fix-design-G1.1]

DECISION: the tier-4 datastore-crossing case's trigger becomes an UNANSWERED compensating `Shutdown` (the fixture fails the compensating RPC), not "the adapter refuses" — BECAUSE after the amendment a refusal IS `SUPERSEDED`/`ABSENT`, which CODE-4 maps to `leaked=false` (non-spec-changes.md:568-575), the opposite of what the case asserts; the unanswered trigger takes CODE-4's `err != nil` arm (:565-570) and is the vocabulary SPEC-2's §7.1 paragraph uses ("a reclaim that did not complete", spec-changes.md:392) and the tier-1 accounting cases already use ("unacknowledged", :1530,:1550,:1567) — ALTERNATIVES: (a) `RECLAIMED` without a clean exit, also `leaked=true`, rejected because CODE-5's own text grounds the exclusion in "went unanswered at all" (:958-960) and it needs a second fixture seam (an erroring runtime Close); (b) deleting the paragraph, rejected — it is the only tier-4 coverage of the Redis-counter/`SandboxClaim` crossing and the `ExcludePods` re-bind.

FACT: the tier-4 fixture can drive an unanswered reclaim with no new harness. `recycleAdapterDialer` builds the client itself and `adapterclient.Dial` is variadic over `grpc.DialOption`, so a unary client interceptor that fails only `Shutdown` drops in. EVIDENCE: tests/tier4_integration/recycle_scrub_path_test.go:203-219; pkg/gateway/runtime/adapterclient/client.go:48-55.

FACT: a §16.3 span category IS assertable inside package `adapter` at tier 1. `tracing.RecordError` puts the category on the span as the `error.category` attribute, and `pkg/adapter/tracing_internal_test.go` (package `adapter`) already ships `installInternalSpanRecorder`/`endedSpanNamed`. EVIDENCE: pkg/observability/tracing/tracing.go:202-213,:107; pkg/adapter/tracing_internal_test.go:55-61,:67-86.

DECISION: the missing `slotResolveCategory` transient arm is closed by extending the existing five-resolve-site bullet (:1380-1386), asserting `error.category` = TRANSIENT on the sentinel and PERMANENT otherwise for the three rows that stamp a category, and adding §16.3 to that case's annotation — BECAUSE asserting `slotResolveCategory` in isolation pins the helper but not that `staging.go`'s three sites stopped passing the literal `tracing.CategoryPermanent`, which is exactly the ship-it-broken path the finding names. ALTERNATIVES: a separate new case, rejected as duplication of an already table-driven bullet.

WATCHOUT: the other twenty "refuse/refused" hits in `## Testing` (1351-1804) are the reclaim-hold and epoch refusals and are correct post-amendment. Only :1715 and :1722 carry the pre-amendment sense. Do not sweep the word blindly. EVIDENCE: non-spec-changes.md:1351,:1362,:1777,:1800.

UNVERIFIED: the "Files touched" spec-map bullet (:2025-2031) lists new `tests/` surfaces only and names no entry for the new `pkg/adapter/bindepoch_test.go`, though spec-map does carry `pkg/adapter/*_test.go` rows (tests/spec-map.json:970). If a §16.3 annotation lands on that file, somebody should confirm whether `lenny-test validate-maps` wants a section-16.3 row for it.


### [non-spec-recheck.4.fix-design-G2.1]

FACT: `tests/spec-map.json` is NOT the only register a new test file must enter. `tests/tier0_static/spec_map_slot_address_registration_test.go` holds a hand-written `slotAddressCaseFiles` inventory (233 entries) and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` fails tier 0 for any test file under cmd/, migrations/, pkg/, scripts/, sdks/, tests/ that (1) matches `slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` OR (2) contains a call matching `slotSurfaceCallRE` = `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(` OR (3) sits under tests/tier11_docs/ and reads TEST-GAPS.md. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1131,1173,1182,1191,1211-1228
FACT: the inventory is exhaustive today (I enumerated every tracked `_test.go` under the six walk roots; none matching the derived rules is missing), so the gate is live rather than dormant. Two of 0081's new files match rule 1: `tests/tier7a_load_local/slot_reclaim_hold_race_test.go` and `tests/tier9_security/slot_credential_reclaim_fence_test.go`. The other new files (`pkg/adapter/bindepoch_test.go`, `tests/tier3_contract/adapter_bind_epoch/`, `tests/tier10_conformance/bind_epoch_conformance_test.go`) match none as described.
WATCHOUT: rule 2 is a text match over the file BODY, so an EXISTING touched test file outside the inventory becomes required the moment an edit adds one of those call spellings. Three touched files are outside the inventory today and carry zero such calls: `pkg/adapter/socketruntime_test.go`, `pkg/gateway/sessionserver/start_test.go`, `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`. CODE-5 edits `start_test.go` around `bindConcurrentSlot`'s reserved branch, which is the plausible place a `BindReservedSlot(` literal appears. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1215-1219
FACT: entering the inventory is not free. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:970-988) then requires tests/spec-map.json to credit the file under EVERY section its own `// spec:` annotation names, and `TestAddressCaseNamesAgreeWithTheirOwnCitations` (:1025-1038) requires a `_spec_X_Y` function-name suffix to agree with that annotation. So the tier-7a file's annotation must be `§5.2` alone (its staged case name is `..._spec_5_2` and its staged spec-map entry is 5.2 alone).
DECISION: fix by registration, in four places — the two new-file subsections (non-spec-changes.md:1778, :1824), the `Tests:` line of `## Files touched on application (non-spec)`, and the S10 checklist line — plus one general sentence covering the call-derived rule. BECAUSE the gate is a shipped tier-0 check S10 runs. ALTERNATIVES: renaming the new files out of the regexp (evades a gate the files genuinely fall under); relaxing the gate (out of scope, weakens a shipped check); registering only in files-touched without the checklist clause (the implementor works the checklist).


### [non-spec-recheck.4.review-applicability.1]

FACT: A live tier-0 gate nobody in this loop has named yet. `tests/tier0_static/spec_map_slot_address_registration_test.go` holds a hand-written inventory `slotAddressCaseFiles` (:236) and a derived completeness rule `slotSubjectFileRE = (slot|one_session_only|sole_session)[^/]*_test\.go$` (:1173). `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108-1131) fails for any test file under cmd/ migrations/ pkg/ scripts/ sdks/ tests/ whose basename matches that regexp and is absent from the inventory. I verified the gate is enforced today: all 35 currently-matching tracked files are present in the list, none missing. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1129, :1173, :1191

WATCHOUT: This proposal creates exactly two files that match and names the gate nowhere — `tests/tier7a_load_local/slot_reclaim_hold_race_test.go` and `tests/tier9_security/slot_credential_reclaim_fence_test.go`, both at S10, which declares Tier 0. Any future proposal creating a `slot*_test.go` pays the same toll, and the same file also requires (`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`, :970) that every inventoried file be credited in tests/spec-map.json under EVERY section its own `// spec:` annotation names, and (`TestAddressCaseNamesAgreeWithTheirOwnCitations`, :1025) that a `_spec_X_Y` name suffix agree with its annotation. EVIDENCE: non-spec-changes.md:1785, :1826; implementation-checklist.md S10

FACT: Test files are OUTSIDE the change-graph completeness check (`changegraph.IsSourceFile` returns false for `_test.go`), and `pkg/adapter/` is already a coverage-baseline prefix, so the one new production file `pkg/adapter/bindepoch.go` needs no `tests/change-graph.json` edit. Do not file that. EVIDENCE: cmd/lenny-test/changegraph/changegraph.go:59-62; tests/registers/change-graph-coverage.yaml:43

FACT: Verified clean, so nobody re-derives them. All nine SCHEMA-1 field numbers are free on their messages (ShutdownRequest holds 1,2,3,5,6 with 4 reserved; AssignCredentialsResponse is `{}`). `tests/tier0_static/adapter_proto_message_scope_test.go` (the §4.1 address gate), `adapter_session_address/session_address_wire_test.go`, `checkpoint_stream_wire_test.go` and `claim_register_proto_agreement_test.go` are all unmoved by additive int64/enum fields. `tests/registers/identifier-senses.yaml` is keyed by position among RETIRED channel spellings only (`LifecycleChannel`, `controlchannel`, `lifecycle-socket`, ...), none of which the staged text introduces, so no occurrence index shifts. EVIDENCE: schemas/lenny-adapter.proto; tests/tier0_static/identifier_resolution_test.go:64-72 of §28.3's naming table

FACT: The checklist is clean as an execution sequence at fourteen steps. 15 deliverables over 14 steps (S6 carries DOCS-1+DOCS-2, both docs lane), none named twice, none unstaged, every `Depends on` naming an earlier step, one lane per step, all spec steps leading (S1-S5), no checked box. Lane vocabulary `spec|code|schema|migration|test|docs` is what the skill accepts. EVIDENCE: .claude/skills/implement-proposal/SKILL.md:17-21

FACT: SPEC-4's fenced edge is spelled `receiving_uploads ──→ slot_cleanup` with the box-drawing arrow, which is exactly the string the tier-11 gate's `generalSlotEdges` slice needs; the prose paragraph's single-arrow `receiving_uploads → slot_cleanup` sits after `generalHeader` so it never lands in the negative `scopedBlock` check. Do not file an arrow-spelling mismatch. EVIDENCE: spec-changes.md:641; tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, :55, :63

UNVERIFIED: The claim-register row SCHEMA-1 stages names the surface symbol `Server.nextBindEpoch`, while CODE-6 stages `nextBindEpochLocked`. No gate validates a surface symbol's existence (only the credential rows get a reachability gate), so I judged it below the bar and did not file it. Someone consolidating the register rows may want to align the spelling. EVIDENCE: non-spec-changes.md:1236; non-spec-changes.md:1005 (`nextBindEpochLocked`); tests/tier0_static/claim_register_test.go:29-45


### [non-spec-recheck.4.review-citations.1]

FACT: the round-4 snapshot and the working tree are byte-identical except the review log, so no staging text changed since non-spec-recheck-r3. `diff -ru scratchpad/cp-snap/.../non-spec-recheck-r4 proposals/0081_...` is empty and `diff -rq non-spec-recheck-r3 non-spec-recheck-r4` differs only on `.review-log.md`. Do not spend a round hunting a delta that is not there. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-recheck-r4

DECISION: returned an empty findings list — BECAUSE I re-verified essentially every `file:line` citation in the non-spec staging and the summary against the tree and every one resolved, with only sub-three-line drift that changes no meaning — ALTERNATIVES: filing the tier-4 "releases the slot with leaked=false" attribution (concurrent_workspace_test.go stands up no gateway Binder, SlotClaimer or Redis) and the CODE-6 test-call-site enumeration gap; both are the test-plan-precision family this loop has refuted at least four times, and both files are already in "Files touched on application".

FACT: `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` on one line at schemas/lenny-adapter.proto:1033, so `bind_epoch = 1` is correct. A naive `awk '/^message X \{/,/^\}/'` runs past it into `RotateCredentialsRequest` and makes it look like fields 1,2,3,5 are taken. Verify one-line messages with a brace-depth walker. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: every field number SCHEMA-1 claims is free is free. ShutdownRequest holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1609-1636); ShutdownResponse 1,2 (:1665-1668); PrepareWorkspaceResponse 1,2 (:699-702); FinalizeWorkspaceResponse 1 (:761-768); ResumeResponse 1,2,3 (:1433-1447); ConfigureWorkspaceResponse 1 (:1690-1692); RunSetupResponse 1 (:869-871); StartSessionResponse 1 (:958-962). The enum prefix convention matches `SessionScrubOutcome` at :438-449.

FACT: the claim-register validator accepts the two staged rows as written. A `WIRED` row must carry no `deferral_id` and must name a file-or-symbol surface rather than a bare line; `#2851-gateway-to-pod` resolves against spec/28's headings. EVIDENCE: tests/tier0_static/claim_register_test.go:276-290. The separate proto-agreement gate only tracks rows whose `surface` contains the adapter proto path and whose claim is `Message.field`-shaped, plus a coverage rule scoped to `coordination_generation` alone, so neither staged row and none of the nine fields trips it. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:66-102

FACT: `tests/spec-map.json` already carries `4.7.1` as its own section key (alongside 4.7.2 … 4.7.11), so S9's directory entry under sections 4.7, 4.7.1, 7.1 and 15.4 resolves. Tier 3 admits a directory entry (`tests/tier3_contract/adapter_checkpointbarrier/...` is the precedent); tier 7a, tier 9 and tier 10 are mapped file by file.

WATCHOUT: `Binder.releaseCredentials` releases only `b.Credentials` (the pool assigner); `UserCredentialAssigner` exposes `MintProto` and no release at all, so the user-source leases `assignSlotCredentials` mints in the same stage are never returned. CODE-4's "the failed attempt returns its own leases here" is therefore true of the pool half only. This is shipped behaviour on every existing release site (binder.go:1077, :1101, :1963), there is no release surface to call, and CODE-4 invents nothing — I judged it below the bar. A future round that wants to file it must argue from a harm the shipped paths do not already have. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:346-348, :1263-1268, :1248

WATCHOUT: §4.7.1's caller rule says a caller holds no epoch "once it has itself issued an RPC on that connection that removes the entry the epoch names", naming `DemoteSDK` and "a `Shutdown` answering `reclaimed`". CODE-6 clears the latch only on `DemoteSDK` and on `ShutdownReclaim` answering RECLAIMED; the plain `Client.Shutdown`/`ShutdownRecycle` return `(bool, error)` and cannot read `slot_reclaim`, so they leave a stale latch. Every such caller closes or abandons the connection immediately after (slotbinder.go:542-544 defers `Adapter.Close()`), so I found no reachable consequence and did not file. Somebody re-deriving the latch contract should confirm that holds for `cmd/lenny-gateway/user_revocation.go:129`.

FACT (spot-check ledger, all verified this round, so do not re-verify unless the tree moves): pkg/adapter/session.go 133/147/157/163/238/243/259/260/271/291 and drainReason at :314; slotsession.go 75/87-88/174-188/192/214-220/238-260/274-283/310-313/404; slot.go 105-125/135-147/208-212; runtimegeneration.go 26-49/58-69/82-88; resume.go 25-35/33/42/50/101-104/140/144; sdkwarm.go 217/261/297; socketruntime.go 156-162/181/184/199-204/219/371-378/398-417/435-466; embedded.go 188-204; mcpruntime.go 266-291; holdstate.go 89-99/177-190/229-262; checkpoint.go 115; oplock.go 36-40/82; coordination.go 116/120/133/262/281; slotfailure.go 41-48/74/91-101; slotbinder.go 210-224/230-254/265-357 (five `cl.Close()`)/430-441/449-473/487-505/528-546; binder.go 293/301/321-323/843-965 (close at :957)/1009/1093-1106/1263-1268/1323; start.go 2140/2148/2172/2594-2608/2720/2761-2779/2807-2809/2830-2848/2873-2879/3609/3648-3681/4005/4040-4042; slotclaimer.go 336-357/416+/510-522/830-843/845-847/850-878/881-885; scrubreport_server.go 96-112/451-481; upload_to_session.go 110-136; executor/pod.go 312-328; coordination_seams.go 215-249; user_revocation.go 45/50/55/128-129; slotstate.go 90-114; registry.go 6-14/97-116; spec/04:151-157; spec/05:453/545/549/551/553-556; spec/06:80/150-156/229/234; spec/10:30/66-68; spec/28:1082; schemas/runtime-ops-events.schema.json:174-185; docs/reference/adapter-contract.md:10/64/76; docs/reference/state-machines.md:228-237; tier3 shutdown_recycle_wire_test.go:208/258-267; tier11 per_slot_substate…:32-37/55/69/102-109 and basic_level_echo_stamp…:284-311.


### [non-spec-recheck.4.review-client-surface.1]

DECISION: Returned an empty findings list for the client-facing-surface lens — BECAUSE every parallel representation of every surface the proposal opens was verified present and consistent — ALTERNATIVES: filing the "StartSession response epoch missing from the Files-touched enumeration for session.go" and the "ensureSlotPaths/claimSessionSlot call sites in export_test.go, slotsession_test.go and podmcp_arming_internal_test.go not named in the widened-return sentence" items; both were rejected because every affected file is already in the `## Files touched on application (non-spec)` list and the loop already refuted that exact granularity complaint (review-log-archive: the `deregisterSlot` call-site finding).

FACT: `AssignCredentialsResponse` really IS an empty message, at schemas/lenny-adapter.proto:1033 (`message AssignCredentialsResponse {}` on one line), so the proposal's "its first field is 1" is correct. WATCHOUT: an `awk '/^message X \{/,/^\}/'` range over this proto silently runs past a one-line empty message into the NEXT message's fields and makes it look like the message is occupied. Use `grep -n "^message X"` then a targeted `sed` range instead. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: all nine SCHEMA-1 field numbers are free on their messages, verified by direct read: ShutdownRequest holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1609-1636) so 7 is next; ShutdownResponse 1,2 (:1665-1668); PrepareWorkspaceResponse 1,2 (:699-702); FinalizeWorkspaceResponse 1 (:761-768); RunSetupResponse 1 (:869-871); StartSessionResponse 1 (:958-962); ResumeResponse 1,2,3 (:1433-1447); ConfigureWorkspaceResponse 1 (:1690-1692). The `SLOT_RECLAIM_OUTCOME_*` spelling matches its sibling `SESSION_SCRUB_OUTCOME_*` at :438-449.

FACT: the adapter proto has NO parallel representation outside `schemas/` and the generated `pkg/proto/adapter/v1`. `grep -rln "ShutdownRequest\|SessionScrubOutcome" sdks/ docs/ charts/ pkg/embedded` returns only schemas/lenny-adapter.proto. §28.7's wire-contract artifact register is per-artifact, not per-field (spec/28_communication-channels.md:1759-1782), so an additive field needs no register row there. The only reader-facing mirror of the gateway→adapter RPC contract is docs/reference/adapter-contract.md, which DOCS-2 covers.

FACT: `cmd/lenny-compliance` parses the embedded proto but only inside `enum ErrorCode { ... }` (cmd/lenny-compliance/schemaassert.go:54, regex `errorCodeEnumBlock`), so a new top-level enum cannot disturb it.

FACT: `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is the ONLY closed field set over any message SCHEMA-1 opens — verified by grepping every one of the seven response messages plus ShutdownResponse across tests/ (no hits except this file and non-assertive uses). The proposal's claim to that effect is true. EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208, :259, :266

FACT: the DOCS-2 replacement `Shutdown` row still contains all four substrings the shipped tier-11 gate requires ("end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub"). EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311

FACT: the unedited `ReportSessionScrub` row in docs/reference/adapter-contract.md:81 ("at each session release") does NOT become false under SPEC-3's withheld-report rule, because SPEC-3 frames the pre-`running` reclaim as an ADDITIONAL trigger of the per-slot cleanup rather than as a session release ("A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome"). I checked this as a candidate unstaged-doc-site finding and it does not hold.

FACT: both gateway callers of `PrepareWorkspace` always send at least one upload frame, so the delta's "every response reports a non-zero epoch" conclusion is true even though its stated reason names only `stageWorkspace`. The second caller is the §7.4 mid-session path, which rejects `len(req.Files) == 0` with a 400 before dialling. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:183-186; pkg/gateway/podlifecycle/podsession/binder.go:1327

FACT: the proposal's client-error claim that an all-candidates-excluded retry yields `WARM_POOL_EXHAUSTED` with `details.reason: "concurrent_slots_exhausted"` is true: `SlotClaimer.ClaimSlot` returns `ErrNoIdlePod` only when `len(list.Items) == 0`, and the excluded pod is itself in the pool list, so the fall-through is `ErrNoConcurrentSlot`. EVIDENCE: pkg/gateway/podclaim/slotclaimer.go:515-522 (pkg/gateway/podlifecycle/podclaim/slotclaimer.go); pkg/gateway/sessionserver/start.go:185

UNVERIFIED: CODE-4's outcome switch routes `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` (a third-party adapter that leaves the field unset) with `exited_cleanly == false` into the `default: return false` arm, i.e. not-leaked. First-party adapters always set the field and §15.4 makes the three outcomes normative, so I judged this below the bar. A later reviewer who thinks an unset-outcome answer should be conservative (leaked) should weigh it. EVIDENCE: non-spec-changes.md:563-582


### [non-spec-recheck.4.review-docs-alignment.1]

DECISION: empty findings list for the documentation-alignment lens on the non-spec staging — BECAUSE the delta this round is DOCS-2 (brand new since this lens last converged), CODE-5's `isTransientPodClaimError` `codes.Aborted` arm, SCHEMA-1's tier-3 closed-field-set note and the claim-register generator note, and I verified every one of them against `docs/`, `tests/tier11_docs/` and the tree without finding a page that becomes wrong and is unstaged — ALTERNATIVES rejected: (1) `docs/reference/adapter-contract.md:81`'s `ReportSessionScrub` row, barred by the standing dead-end at review-log.md:534/:562 and still correct because SPEC-3 places the pre-`running` cleanup outside "session release"; (2) `docs/api/internal.md:488-499`'s gRPC status table gaining `ABORTED`, barred by the standing entry at review-log.md:645 (the table already omits `ABORTED` and `INVALID_ARGUMENT`, both of which the shipped adapter returns, so the omission is pre-existing); (3) `docs/reference/error-catalog.md:143` `WARM_POOL_EXHAUSTED` ("No idle pods are available") against CODE-5's `ExcludePods` exclusion, rejected because the row is already approximate for the shipped `concurrent_slots_exhausted` case and the proposal mints no new code or client-visible category; (4) the bind-epoch block omitting "zero is not an epoch", rejected as incompleteness on a page written for runtime authors rather than adapter authors.

FACT: DOCS-2's staged text clears every shipped tier-11 gate and every substring the staged tier-11 extension adds. The gate is `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311), reading the row with `lineContaining(page, "| \`Shutdown\` |")` (:298) and requiring "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" (:302-307) — all four present in the staged row. The two new substrings ("no other bound session", "a session whose start the adapter has admitted") and the two page-level ones ("bind epoch", "reclaim hold", the latter inside the block heading "**Bind epoch and slot-identifier reclaim hold.**") are all present verbatim. EVIDENCE: non-spec-changes.md:1300, :1306; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:298-307

FACT: DOCS-1's tier-11 plan verifies line-for-line. `generalSlotEdges` is at tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, the positive loop over the either-concurrency block at :55, the negative loop over the concurrent-occupancy block at :70, and the reference-page `requireAllContain` over `section(doc, "Per-slot sub-states")` at :103-110. The insertion point docs/reference/state-machines.md:235 (`receiving_uploads` -> `running`) is unique and the paired substring `` `receiving_uploads` | `slot_cleanup` `` occurs nowhere on the page today. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,55,70,103-110; docs/reference/state-machines.md:232-237

FACT: DOCS-2's `DemoteSDK` amendment is true against the tree, not merely against §4.7.1's caller rule. `Server.DemoteSDK` resolves the registry's single entry via `anyRegisteredSession` and calls `releaseSessionSlot`, which deregisters it, so "drop the adapter's slot registry entry for the session" and "the next bind sequence on the pod mints a fresh bind epoch" both hold. EVIDENCE: pkg/adapter/sdkwarm.go:296-301, :309-316

FACT: CODE-5's new `codes.Aborted` arm moves the code TOWARD an existing doc rather than away from it, so it owes no docs edit. `docs/reference/error-catalog.md:157` already publishes "Retryable: the session row stays in `awaiting_client_action` so the explicit resume retry succeeds once the condition clears" for `RESUME_FAILED`, while shipped `isTransientPodClaimError` falls through to `return false` (terminal `failed`) for a bare status. The arm's cited anchor resolves exactly: pkg/gateway/sessionserver/start.go:3648-3681, switch closing at :3679 and `return false` at :3680. EVIDENCE: docs/reference/error-catalog.md:157; pkg/gateway/sessionserver/start.go:3648-3681

FACT: the `docs/` blast radius is still exactly the two staged files, re-derived this round rather than inherited. `grep -rn "Shutdown" docs/ --include=*.md` outside `adapter-contract.md` returns only the runtime-facing JSONL `shutdown` frame (getting-started/concepts.md:134, runtime-author-guide/testing.md:28,:220). No docs page anywhere names `ShutdownRequest`/`ShutdownResponse`/`SessionScrubOutcome`, so SCHEMA-1's nine fields and its enum have no docs mirror. No docs page enumerates the per-slot cleanup's action list, so SPEC-3's two added actions have none either. `lenny_adapter_leaked_slots` appears in no `docs/` page and in no `pkg/alerting/` rule, so the new `leaked` cause owes no metrics or runbook companion. EVIDENCE: grep over docs/ and pkg/alerting/; BUILD-GAPS.md:4963 records the gauge's deliberate absence from the §16.1 catalog

WATCHOUT: do not re-derive the audience mismatch in DOCS-2's rationale ("so the epoch and the hold reach the third-party adapter author the page is written for") against docs/reference/adapter-contract.md:10, which calls itself the reference for "the protocol between the Lenny adapter sidecar and **your runtime binary**". It is already recorded at review-log.md:2138 as deliberately not filed: the staged block's content is accurate and lands, and the sentence is proposal prose that never reaches `docs/`.


### [non-spec-recheck.4.review-edit-sites.1]

FACT: the r4 snapshot at scratchpad/cp-snap/.../non-spec-recheck-r4 is byte-identical to the live proposal, so `diff -ru` returns nothing. The real delta is the UNCOMMITTED working-tree change against HEAD c5900230f — use `git diff HEAD -- proposals/0081_*/` instead. EVIDENCE: git status --porcelain shows four modified 0081 files.
FACT: the r4 delta is four things — (1) the `sbe.Leaked` / `sbe.Leaked || relErr != nil` predicate split between placement and accounting (non-spec-changes.md:776-782, :836-841, :878-879) plus its new tier-1 case (:1555-1560); (2) `resolvePrepareStagingDir` widening and the PrepareWorkspace epoch (:1031-1041); (3) the S9/S10/S11 attribution of the tier-1 bindepoch cases (:1317-1322 and the S-prefixed bullets); (4) the summary's consolidation of four 0080 rows into one (summary.md ~:644).

FACT (verified, do not re-derive): every SCHEMA-1 field number is free on its message — ShutdownRequest holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1609-1636); ShutdownResponse 1,2 (:1665-1668); PrepareWorkspaceResponse 1,2 (:699-702); FinalizeWorkspaceResponse 1 (:761-768); ResumeResponse 1,2,3 (:1433-1447); ConfigureWorkspaceResponse 1 (:1690-1692); RunSetupResponse 1 (:869-871); AssignCredentialsResponse empty (:1033); StartSessionResponse 1 (:958-962).
FACT: `tests/claim-map.json` is 76 rows / 32 WIRED / 24 UNWIRED / 20 ABSENT today, exactly as the summary's 0080 row states, and 0080 §1.12:163 and §1.18:305-313 carry "seventy-six", "thirty-two", "twenty", "twenty-four". Verified; no need to recount.
FACT: `_test.go` files are OUTSIDE the change-graph completeness domain (cmd/lenny-test/changegraph/changegraph.go:59-62), and the spec-map orphan walk covers only tier dirs at or above component (cmd/lenny-test/cmd_validate.go:747-760). So new tier-1 test files (`pkg/adapter/bindepoch_test.go`) need NO spec-map entry and NO change-graph glob, and the new dir `tests/tier3_contract/adapter_bind_epoch/` needs no change-graph glob either. `pkg/adapter/` is already a coverage-baseline prefix (tests/registers/change-graph-coverage.yaml:43), so `pkg/adapter/bindepoch.go` is covered. Three separate gate worries, all dead.
FACT: sections 4.7, 4.7.1, 5.2, 6.2, 7.1 and 15.4 all already exist as keys in tests/spec-map.json, so SCHEMA-1/CONF-1's map entries need no new section key and collide with no spec-map exception.
FACT: the tier-11 gate that reads the §4.7 `Shutdown` row (tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:138-141) requires the row to still contain the literal link `05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes`. SPEC-1's replacement row keeps it (spec-changes.md:322). Do not re-check.
FACT: `Client.PrepareWorkspace` with an empty uploads map sends zero frames (pkg/gateway/runtime/adapterclient/client.go:247-264), but BOTH production callers guarantee at least one — binder.go:1327 is inside `if len(uploads) > 0`, and upload_to_session.go:128's uploads map is built from `req.Files`, which `parseUploadToSession` rejects when empty (upload_to_session.go:183-186). So the proposal's "every response it sends reports a non-zero epoch" is TRUE even though its stated rationale names only `stageWorkspace`.
FACT: `// spec:` annotation parsing is `\b\d+(?:\.\d+)*\b` over the whole line (cmd/lenny-test/gotest_json.go:149), so the proposal's `// spec: §4.7; §5.2; §7.1` form parses fine. Not a finding.

WATCHOUT: `ensureSlotPaths` has FIVE test call sites across THREE files, not one — exportpaths_test.go:25,:221,:225, export_test.go:62, slotsession_test.go:319. `claimSessionSlot` has FIFTEEN test call sites across THREE files — one_session_only_test.go:91,94,125,159,194,209,212, podmcp_arming_internal_test.go:74,93,138,157,179,187,224, export_test.go:41. non-spec-changes.md:1031-1032 names only two of those five files. EVIDENCE: pkg/adapter/export_test.go:41,:62; pkg/adapter/slotsession_test.go:319.

OPEN: which step routes `Shutdown` through `reclaimSlotLocked`? The checklist S9 line and CODE-6:992-1004 say CODE-6 does; CODE-1/S10 and the new Testing bullets at :1355 and :1366 say CODE-1 does. Both filed as one finding; whoever fixes it must pick one owner and make the S9 parenthetical read as a rule statement or drop `Shutdown` from it.


### [non-spec-recheck.4.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor-action assignment in the staged
non-spec changes and summary resolves against a real symbol with a real capability, and the
assignments the lens governs (spec/04 §4.6.3 ownership, §13.2 egress, §10.3 agent-pod posture,
webhook purity, deployment-model process boundaries, spec/18 phase ordering) are not engaged by
this proposal: everything it stages is gateway↔adapter in-process or over the existing adapter
gRPC connection, with no new apiserver read/write, no new egress, and no controller status write.
ALTERNATIVES: the two candidate filings I developed and dropped are recorded below.

FACT: the r4 staging is byte-identical to the r3 snapshot except the review log.
`diff -rq scratchpad/cp-snap/.../non-spec-recheck-r4 proposals/0081_...` is empty and
`diff -rq .../non-spec-recheck-r3 ...` differs only on `*.review-log.md`. The orchestrator's
"the staging changed after the last convergence" premise did not hold for this firing; there
was no delta to look at first. A future recheck agent should run the snapshot diff before
planning its reading order rather than trusting the prompt's delta claim.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-recheck-r4

FACT: naive `awk '/^message X \{/,/^\}/'` over schemas/lenny-adapter.proto is unsafe for field-
number checks. `message AssignCredentialsResponse {}` is a single line at
schemas/lenny-adapter.proto:1033, so an awk range starting there runs to the close of the NEXT
message and reports RotateCredentialsRequest's fields (1,2,3,reserved 4,5) as
AssignCredentialsResponse's. I nearly filed "field 1 is taken" off that. Use a brace-depth
scanner. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: all nine SCHEMA-1 field numbers re-verified free by brace-depth scan:
ShutdownRequest 7 (holds 1,2,3, reserved 4, 5, 6 — :1609-1635); ShutdownResponse 3 (:1665-1667);
PrepareWorkspaceResponse 3 (:699-701); FinalizeWorkspaceResponse 2 (:761-767);
ResumeResponse 4 (:1433-1446); ConfigureWorkspaceResponse 2 (:1690-1691);
RunSetupResponse 2 (:869-870); AssignCredentialsResponse 1 (empty, :1033);
StartSessionResponse 2 (:958-961). Do not re-derive this.

FACT: the following citations in the non-spec staging and the summary were spot-verified true
and need no re-checking: `resumeOnPod`'s Resume failure branch at pkg/gateway/sessionserver/start.go:4041;
`s.podBinder/s.slotHealth/s.slotStates/s.slotReplacement/s.slotLeakGauge` all exist with the exact
types `accountSlotFailure` would need (start.go:2736, :2807); `classifySlotBindFailure` by value at
:2761 with its two production callers at :2172 and :2602; `maxSlotRetries = 1` at :2720 and the loop
at :2809; `isTransientPodClaimError` at :3648-3681 is a switch of typed errors and sentinels with a
final `return false`, so the `codes.Aborted` arm inserts exactly as staged; `rollbackClaim` at
:3241-3247; the five `cl.Close()` calls inside `materializeSlot` at slotbinder.go:286,293,301,308,321;
`b.releaseCredentials` at binder.go:1263; `releaseResumeSlot` at binder.go:1710 and `Binder.Resume`'s
failure branch at :1620-1630; `ClaimSlot`'s connect-stage release at slotbinder.go:172 and
`BindReservedSlot`'s at :217; `SlotBindRequest.CleanupTimeoutSeconds`/`MaxConcurrentSessions` and
`ResumeRequest.CleanupTimeoutSeconds`(:667)/`MaxConcurrentSessions`(:645)/`SessionID`(:606);
`SlotBindError.Reason()` has no Aborted case and defaults transient (slotfailure.go:85-101);
`handleUploadToSession` answers 502 UPSTREAM_ERROR on both PrepareWorkspace and FinalizeWorkspace
(upload_to_session.go:128-139) and builds no SlotBindError; `PodExecutor.Release` calls
`registry.Remove` then `binder.ReleaseSlot` (executor/pod.go:312-321); the three `podRegistry.Put`
production callers (start.go:2936, :4057, cmd/lenny-gateway/coordination_seams.go:249) with the
re-adopt sending CoordinatorFence as its first RPC (:218-233); §7.1 step 23 really is "Release
credential lease back to pool" (spec/07:44); remediation rule S-2's second-window sentence
(gateway-runtime-comms-remediation.md:1883-1890) and its covered-file list, of which this proposal
opens exactly three (session.go, slotcreds.go, sdkwarm.go); tests/claim-map.json holds 76 rows,
20 ABSENT / 24 UNWIRED / 32 WIRED, matching the summary's 0080 §1.12 arithmetic; EXPLICIT WIRED rows
with no deferral_id already exist (scripts/seed-claim-register.py:208-234); the adapter-contract
page's self-description at :10, DemoteSDK row at :64, Shutdown row at :75, scrub-responsibilities
paragraph at :84; `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`'s four required
substrings (tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:305-310) all survive
DOCS-2's staged row; `generalSlotEdges` at per_slot_substate_scope_doc_reconciliation_test.go:32-37
with the positive loop at :55 and the negative loop at :70, and spec/06:146-156's scoped-then-general
block order, so DOCS-1's edge lands in the general block the positive loop reads.

FACT: the §7.4 mid-session upload can never call `PrepareWorkspace` with zero uploads —
`parseUploadToSession` rejects `len(req.Files) == 0` with a 400 (upload_to_session.go:183-186) —
so CODE-6's "every call the gateway makes carries at least one upload frame" holds for BOTH
PrepareWorkspace callers, not only `stageWorkspace`. I checked this because `Client.PrepareWorkspace`
sends no frame at all for an empty map (adapterclient/client.go:252-258), which would have made the
response report a zero epoch that staged §4.7.1 calls non-conforming. It cannot happen.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:183

FACT: every production and test caller of `claimSessionSlot`, `ensureSlotPaths` and
`noteRuntimeStarted` lives in a file the "Files touched on application" list already names
(including `slotsession_test.go:319` and `export_test.go:62`, which CODE-6's prose does not
enumerate by name). A widened-signature missing-edit-site finding therefore does not clear the bar
on this proposal; it is the same shape the loop already refuted for `deregisterSlot`.

MISTAKE (mine, caught before filing): I started to file that §6.2's `slot_cleanup ──→ leaked`
edge annotation, "cleanup timeout exceeded — slot not reclaimed until pod termination"
(spec/06_warm-pod-model.md:148), is narrower than the leak triggers SPEC-2/SPEC-3 introduce (an
unanswered reclaim, a `reclaimed` answer without a clean exit) and is in no edit list. It does not
clear the bar: the shipped tree already produces that transition from a failed reservation release
(pkg/gateway/sessionserver/start.go:2834-2848), so the annotation is already narrower than shipped
behaviour, and §6.2's own `**leaked** slot semantics` paragraph defines the state generally. Pre-
existing and diagrammatic. A future filing here would need a harm the general paragraph does not
already cover.

MISTAKE (mine, caught before filing): I also started to file that staged §7.1 binds "the creation
finalize block" to the reclaim while no code deliverable compensates it (the exclusive finalize path
goes through `Binder.Prepare`, not `materializeSlot`, because `prepareAtFinalize` returns nil for
`MaxConcurrentSessions > 1`). This exact angle is already in the refuted list — "Staged §7.1 obliges
a pod-side reclaim on the creation finalize block, where no adapter connection is held and
re-dialling is forbidden" — refuted on the ground that the only instance of that path is the
single-session case §7.1's closing sentence carves out and the pod retires. Do not re-file it by
another route.

USEFUL [standing context, "Ownership is clean."]: saved me a full §4.6.3 / RBAC re-derivation. I
confirmed the only cluster-facing action this proposal touches is `DrainSandbox`'s existing
`lenny.dev/drain-request` annotation, reached through the unchanged `slotBinder` interface.


### [non-spec-recheck.4.review-fresh.1]

DECISION: returned an empty findings list for the fresh-holistic lens on non-spec-recheck round 4 — BECAUSE every new claim in the round-3→4 delta verified true against the tree, and the residual imprecisions I found are all inside already-refuted families — ALTERNATIVES: filing the "Seven residues survive" count in summary.md (its own enumeration is 4+2=6); rejected because an identical stale-narrative-count finding ("Four statements are needed while the section now makes six") was already refuted as immaterial in this loop.

FACT: the staging delta for round 4 is tiny and fully enumerated by `git diff` against HEAD (c5900230f). The r4 snapshot at scratchpad/cp-snap/.../non-spec-recheck-r4 is byte-identical to the working tree, so `diff -ru` against it yields nothing; diff against `non-spec-recheck-r3` shows only the review log. Use `git diff proposals/0081_.../*.non-spec-changes.md` instead. EVIDENCE: git status shows 4 modified files under the proposal dir, none committed since c5900230f.

FACT: the round-4 delta is exactly four things — (a) the `ExcludePods` append predicate narrowed from `sbe.Leaked || relErr != nil` to `sbe.Leaked` alone, with the rationale and a new tier-1 case pinning the split; (b) `resolvePrepareStagingDir` widened to `(string, int64, error)` plus the "every gateway call carries ≥1 upload frame" argument and the two retargeted test files; (c) the tier-1 bindepoch_test.go cases split across S9/S10/S11 with **S10.**/**S11.** prefixes, mirrored in the checklist; (d) the four 0080 impact rows collapsed into one. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:777, :836, :1030-1046, :1329-1394; .implementation-checklist.md S9-S11; .summary.md:659.

FACT (verified, saves re-derivation): every SCHEMA-1 field number is free. `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` at schemas/lenny-adapter.proto:1033, so field 1 is correct. WATCHOUT: an `awk '/^message X \{/,/^}/'` range over this file MIS-SCOPES that message, because the one-line `{}` body matches the opener and the range then runs to `RotateCredentialsRequest`'s closing brace at :1059. It looks exactly like a field-1 collision. Grep for `^message X ` and read from there instead. EVIDENCE: schemas/lenny-adapter.proto:1033, :1035-1059.

FACT: the "every PrepareWorkspace call the gateway makes carries at least one upload frame" claim holds on BOTH gateway callers, not only the one the proposal names. `stageWorkspace` guards on `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323) and the §7.4 mid-session path rejects an empty file list before it builds `uploads` (`if len(req.Files) == 0` inside `parseUploadToSession`, pkg/gateway/sessionserver/upload_to_session.go:183, caller at :128). `sendUpload` always sends at least one frame per upload (adapterclient/client.go:267-285). So the zero-frame stream that would report epoch zero is unreachable from production.

FACT: `pkg/adapter/bindepoch_test.go` needs NO `tests/spec-map.json` entry. `validateTestFilesMapped` walks only `componentAndAboveTierDirs()` — tests/tier2..tier12 — so tier-1 package tests under `pkg/` are outside the gate. EVIDENCE: cmd/lenny-test/cmd_validate.go:717-790, :125-139.

FACT: the new source file `pkg/adapter/bindepoch.go` needs no change-graph glob key. `pkg/adapter/` is a directory prefix in the coverage baseline, and a directory entry covers files directly inside it. EVIDENCE: tests/registers/change-graph-coverage.yaml:43; cmd/lenny-test/cmd_validate.go:407-418.

FACT: `tests/claim-map.json` carries exactly 76 rows, 32 WIRED / 24 UNWIRED / 20 ABSENT, so the summary's §1.12 arithmetic (76 → 78) is right.

FACT: the five resolve sites and their span categories in CODE-6's table all match the tree verbatim — staging.go:133-137 (caller stamps Permanent at :79-82), :181-185 (stamped beside the wrap), :337-341 (stamped beside), slotsession.go:75-78 (none), slotcreds.go:26-29 (none).

WATCHOUT: `releaseSessionSlot` still calls no `Runtime.Close` (pkg/adapter/slotsession.go:214-220); only the §10.1.4 site does (pkg/adapter/holdstate.go:243). The round-4 fix rewrote the two tier-1 park/panic bullets into an explicit disjunction that STILL names `releaseSessionSlot` as a candidate `Runtime.Close` park site. It was refuted once as a precision issue; do not re-file it, but an implementor must take the §10.1.4 disjunct. EVIDENCE: non-spec-changes.md:1354-1358, :1379-1382.

UNVERIFIED: the summary's "Seven residues survive" enumerates four accepted failure modes plus two unstaged defects (six). If the seventh is meant to be the never-re-dial hard dependency named in the next sentence, the wording should say so. Nobody has checked which was intended. EVIDENCE: summary.md, `**Decisions.**`, the open-decision-9 bullet.

UNVERIFIED: the `Files touched on application` entry for `pkg/adapter/session.go` does not mention the `StartSession` response's `bind_epoch`, while CODE-6 says all seven handlers report it and SCHEMA-1 gives `StartSessionResponse.bind_epoch` field 2. The file is in the list, so nothing is lost, but the entry is one clause short. Same for the deliverable-index CODE-6 file list, which omits `session.go` and `resume.go`. EVIDENCE: non-spec-changes.md:2046-2047; summary.md deliverable index, CODE-6 row.


### [non-spec-recheck.4.review-kubernetes.1]

DECISION: returned zero findings under the Kubernetes-idiom lens — BECAUSE every Kubernetes surface this proposal touches follows the shipped precedent exactly: `ExcludePods` is a request-scoped read-only placement filter modelled on `SlotRequest.MaxPodUptimeSeconds` (pkg/gateway/podlifecycle/podclaim/slotclaimer.go:225-235), the only status-adjacent write remains `DrainSandbox` → `StampDrainRequest`, a merge-patch of an annotation on the agent Pod under FieldOwner `lenny-gateway` consumed asynchronously by the WarmPoolController (pkg/gateway/podlifecycle/podsession/slotbinder.go:601-603; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:94-106), no Sandbox.status is written, no finalizer is added, nothing new sits on etcd per request, and no synchronous request path waits on a reconcile — ALTERNATIVES: I considered filing the reclaim hold as a hot-path block, but the hold is adapter-process-local and the gateway explicitly stages no in-gateway wait (non-spec-changes.md:947-953).

FACT: the snapshot at scratchpad/cp-snap/.../non-spec-recheck-r4 is byte-identical to the live proposal directory (`diff -rq` returns rc=0), so the prescribed delta diff yields nothing. The real delta for this round is `git diff c5900230f -- proposals/0081_.../` (4 files, ~85 lines in non-spec-changes.md). Use that, not the snapshot. — EVIDENCE: git log --oneline -- proposals/0081_.../ shows c5900230f as the pre-firing-2 baseline

FACT: the delta this round is (1) `ExcludePods` placement predicate narrowed from `sbe.Leaked || relErr != nil` to `sbe.Leaked` alone, with a new tier-1 case pinning the two predicates apart; (2) `resolvePrepareStagingDir` widened and `PrepareWorkspace.bind_epoch` given a route; (3) tier-1 bindepoch_test.go cases assigned per step S9/S10/S11; (4) two adapter test files added to the touched list; (5) the 0080 impacts row consolidated into one entry that now also carries §1.2 and §1.12. — EVIDENCE: non-spec-changes.md:780-790, :1029-1040, :1330-1372, :2083, summary.md 0080 row

FACT: `tests/claim-map.json` genuinely carries 76 rows, 32 WIRED / 24 UNWIRED / 20 ABSENT, so the summary's new §1.12 denominator arithmetic (76 → 78 after SCHEMA-1's two WIRED rows) checks out. — EVIDENCE: tests/claim-map.json, counted by status

WATCHOUT: the narrowed `ExcludePods` predicate means a failed `ReleaseSlotReservation` no longer moves the retry off the pod, and the Redis slot counter is a plain per-pod INCR/DECR with no per-slot idempotence (pkg/gateway/storage/slotcounter/slotcounter.go:272,:470), so the retry re-increments and the pod's count is inflated by one for a single session. This is NOT a regression: before this proposal `ExcludePods` did not exist at all, so the retry always went back to the same pod. The wider predicate would have accidentally masked a pre-existing defect. Do not file it against this proposal. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836 (leaked skips the decrement)

FACT: `ReleaseSlotReservation` hardcodes `recycle=false` into `SlotClaimer.ReleaseSlot` (pkg/gateway/podlifecycle/podsession/slotbinder.go:493-504), so the tier-4 counterfactual "a `leaked=false` release of the pod's last slot deletes the claim" is true even on the recycling `recycle_scrub_path_test.go` pool, where a `recycle=true` release would instead patch the claim to `recycling` (slotclaimer.go:850-878). Anyone re-deriving that sentence should read the recycle argument at the call site before concluding the proposal has the disposition wrong.

FACT: the proposal's reachability argument at `maxConcurrentSessions: 2` — that `DrainSandbox` does not keep the immediate retry off the pod because `ClaimSlot`'s pass 1 reads the per-pod SandboxClaim rather than Sandbox.status.phase — is correct against the tree. Pass 1 filters on claim presence/terminality, tenant pin and `expiredByUptime`, and never on the Sandbox phase. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:417-441; non-spec-changes.md:894-900

FACT: `isTransientPodClaimError` carries only typed errors and sentinels today, so the proposal's premise that a bare `codes.Aborted` falls through to `return false` is accurate. — EVIDENCE: pkg/gateway/sessionserver/start.go:3648-3681


### [non-spec-recheck.4.review-mechanism.1]

DECISION: Returned an empty findings list — BECAUSE every candidate I derived from the round-4 delta verified true against the tree, and the rest of the staging is text seven sweeps have already read — ALTERNATIVES: filing the `SlotReclaimOutcome_UNSPECIFIED` gap in CODE-4's mapping (`non-spec-changes.md:580-582`, `default: return false`), which would record an unclean, outcome-less answer as not-leaked; rejected because the staged first-party adapter always populates the field (CODE-1) and §4.7.1/§15.4 make an UNSPECIFIED answer non-conforming, so the arm is unreachable inside what this proposal ships.

WATCHOUT: the snapshot at `scratchpad/cp-snap/.../non-spec-recheck-r4` was taken at the same instant as the working tree, so `diff -ru` against it is EMPTY and useless. The real delta is the uncommitted working-tree change against `c5900230f`: use `git diff -U8 -- proposals/0081*/0081*.non-spec-changes.md proposals/0081*/0081*.summary.md proposals/0081*/0081*.implementation-checklist.md`. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-recheck-r4 (mtime equals the proposal dir's)

WATCHOUT: do NOT verify a proto message's field numbers with `awk '/^message X \{/,/^\}/'`. `AssignCredentialsResponse` is written as the one-line `message AssignCredentialsResponse {}` (schemas/lenny-adapter.proto:1033), so the range runs on into `RotateCredentialsRequest` and makes the message look like it already holds fields 1,2,3,5. I nearly filed a false "field 1 collides with session_id" finding on that artifact. — EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: all nine SCHEMA-1 field numbers are free on their messages, checked with a brace-counting parser: ShutdownRequest holds 1,2,3,4(reserved),5,6 → 7 free (proto:1609); ShutdownResponse 1,2 → 3 free (:1665); PrepareWorkspaceResponse 1,2 → 3 (:699); FinalizeWorkspaceResponse 1 → 2 (:761); ResumeResponse 1,2,3 → 4 (:1433); ConfigureWorkspaceResponse 1 → 2 (:1690); RunSetupResponse 1 → 2 (:869); StartSessionResponse 1 → 2 (:958); AssignCredentialsResponse empty → 1 (:1033). The `SESSION_SCRUB_OUTCOME_*` spelling the new enum copies is real at schemas/lenny-adapter.proto:438-449. — EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1033,1433,1609,1665,1690

FACT: the round-4 delta's new PrepareWorkspace claim ("every call the gateway makes carries at least one upload frame") holds on BOTH gateway callers, not just the one it names. `stageWorkspace` guards with `if len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323-1327), and the §7.4 mid-session path rejects an empty file list before it ever dials (`if len(req.Files) == 0` in `parseUploadToSession`, pkg/gateway/sessionserver/upload_to_session.go:183, called from :128). `sendUpload` also always emits at least one frame per upload (pkg/gateway/runtime/adapterclient/client.go:267-286). — EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:183

FACT: `ensureSlotStateLocked` has exactly three production callers — `ensureSlotPaths` (pkg/adapter/slot.go:143), `assignCredentialsSlot` (pkg/adapter/slotcreds.go:26) and `claimSessionSlotUnderLock` (pkg/adapter/slotsession.go:75) — so CODE-6's hold refusal reaches no site outside the five the resolve-site table lists. Its test callers (holdstate_test.go:352,:363,:827; usage_test.go:357; one_session_only_test.go:58,:117,:151,:186; manifest_fields_test.go:220) call it directly and its signature does not widen, so they do not break. — EVIDENCE: pkg/adapter/slot.go:105-125

FACT: the round-4 ExcludePods narrowing is sound end to end. `applySlotRetryPolicy` only ever sees a `*SlotBindError` produced by `BindSlot`, never one from `BindReservedSlot` (which `bindConcurrentSlot` handles on its own branch and returns immediately, pkg/gateway/sessionserver/start.go:2594-2605), and it computes `relErr` as a local rather than writing it into `sbe.Leaked` (`:2834`). So at the append point `sbe.Leaked` really does mean "the pod-side reclaim did not complete", which is the predicate staged §7.1 and §5.2's `**Max retries:**` bullet both use. — EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2848

FACT: `tests/claim-map.json` really carries 76 rows, 32 WIRED / 24 UNWIRED / 20 ABSENT, and 0080 §1.12 and §1.18 both say "seventy-six" (proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:163, :307-308). The summary's new consolidated 0080 row is right about §1.12; it does not mention that §1.18's identical denominator also goes stale, which I judged below the bar (it is prose in a Draft inventory, no spec or code surface). — EVIDENCE: proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:307

FACT: `ClaimSlot` returns `ErrNoConcurrentSlot` (not `ErrNoIdlePod`) when the pool has pods but every candidate is skipped and no tenant mismatch was seen, which is what the proposal's queue re-entry argument depends on. Both candidate passes carry an `expiredByUptime` skip at pkg/gateway/podlifecycle/podclaim/slotclaimer.go:433 and :491, so the two new `ExcludePods` skips have a real sibling to sit beside. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:512-520

UNVERIFIED: `reclaimSlotLocked`'s returned `release func()` runs after `s.mu` is unlocked (CODE-1 defers it at non-spec-changes.md:197 then unlocks at :198), but no staged text says whether `release` takes `s.mu` itself. I read it as an implementor's detail rather than a blank over a wire contract or ordering. A later lens on locking discipline should confirm nobody writes it as a bare map delete.


### [non-spec-recheck.4.review-operational.1]

FACT: The r4 snapshot at scratchpad/cp-snap/.../non-spec-recheck-r4 is byte-identical to the live proposal directory except the review log; `diff -rq` against non-spec-recheck-r3 also shows only the review log differing. There was no staging delta to hunt for in this round. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-recheck-r4

FACT: The alert surface is untouched by this proposal and cannot be. `pkg/alerting/rules/rules.go` carries no slot-related rule at all (`grep -n "slot" pkg/alerting/rules/*.go` is empty), and `lenny_adapter_leaked_slots` appears nowhere under docs/ or spec/16 — only spec/06:160 and spec/05:545 name it. Do not spend another round looking for an alert-to-runbook break here. EVIDENCE: spec/06_warm-pod-model.md:160; docs/reference/metrics.md:158-167

FACT: The tier-11 gates that read the two docs pages DOCS-1/DOCS-2 edit are permissive enough that both staged rewrites stay green, and a fix that adds an exception clause also stays green. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only four substrings on the `Shutdown` row (basic_level_echo_stamp_doc_reconciliation_test.go:303-306); `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` pins only the addressing phrase on the `ReportSessionScrub` row (session_scrub_report_addressing_doc_reconciliation_test.go:32); `TestPerSlotCleanupStatedOnEverySessionModeRow` requires only the literal "Per-slot cleanup" in each residual-state row (basic_level_echo_stamp_doc_reconciliation_test.go:473). EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310

FACT: `TestRecycleTriggerCrossRefsResolve_F5215` and `TestRecycleTriggerConsistency` read the spec §4.7 `Shutdown` row and require "recycle disposition", "ReportPodScrub", `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`, "does not block the response on the scrub", and the `#52-pool-configuration-and-execution-modes` link. SPEC-1 replaces only the row's opening and leaves "On the default disposition the pod is replaced." onward intact, so all of them survive. Verified, do not re-derive. EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:60-100; spec-changes.md:324

FILED: SPEC-3 withholds the `ReportSessionScrub` outcome on the pre-`running` path, and three reader-facing sentences state the opposite universally and are in no edit list: docs/reference/adapter-contract.md:81 (the `ReportSessionScrub` row, on the very page DOCS-2 opens, which would then contradict DOCS-2's own `Shutdown` row), docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33. The remedy is entirely non-spec (extend DOCS-2, add a DOCS-3).

WATCHOUT: docs/reference/state-machines.md:251 and spec/06:148 both give `slot_cleanup ──→ leaked` the single trigger "cleanup timeout exceeded", while the shipped gateway already sets `leaked = err != nil || !cleanly` and `applySlotRetryPolicy` already marks leaked on a failed `ReleaseSlotReservation`. That drift is PRE-EXISTING; this proposal widens the producing set (reserved branch, §7.3 re-attach) but the proposal records the widening in the summary's out-of-scope inventory. Do not file it as new. EVIDENCE: docs/reference/state-machines.md:251; pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559

UNVERIFIED: `Binder.Resume` emits no `lenny_slot_failure_total` (no `recordSlotFailure` call anywhere in pkg/gateway/podlifecycle/podsession/binder.go), so CODE-5's new `resumeOnPod` accounting can drive `slothealth` to the unhealthy threshold, drain the pod and increment `lenny_slot_pod_replacement_total` with zero increment on the failure counter an operator would correlate against. I judged this below the bar (no documented statement becomes false) and did not file it. Someone with a stronger read on operator-facing correlation obligations could revisit.


### [non-spec-recheck.4.review-performance.1]

DECISION: filed exactly one finding, on the §7.3-resume accounting sentence in the accepted-failure-modes list — BECAUSE the capacity half of this lens is structurally inert (standing entry: no etcd/Postgres/Redis write, no informer, no watch, no metric series is added; the epoch is an in-memory int64 and the hold an in-memory map key), and the four churn/storm items the log carries are all recorded OPEN/UNVERIFIED and have been declined by four earlier performance passes — ALTERNATIVES: I re-derived and rejected (a) the exclusive-pool `Binder.Resume` budget degeneration to the whole `cleanupTimeoutSeconds` on the gateway request goroutine — §16.5's creation-latency SLO measures only "auth, policy, credential pre-check, pod claim, and Postgres persist" (spec/16_observability.md:626) and excludes this window, so no stated budget is breached; (b) the `removeSlotTree`-outruns-budget → `Leaked=true` → no Redis decrement → pod never retired chain — it is stated verbatim in `compensateFailedSlotBind`'s own doc comment and is §6.2's shipped `leaked` semantics; (c) the blob-outage mass-drain at `maxConcurrentSessions: 2` — CODE-4/CODE-5 state it and review-log.md:608 records it as a nearly-filed MISTAKE with the shipped-baseline rebuttal; (d) a tier-8 gap — declined by three rounds (review-log.md:780).

FACT: `slothealth.Tracker.RecordFailure` and `RecordLeak` have exactly ONE production call site each today, both inside `applySlotRetryPolicy`. — EVIDENCE: pkg/gateway/sessionserver/start.go:2848,:2854; `grep -rn "RecordFailure\|RecordLeak" pkg/ cmd/ --include=*.go | grep -v _test` returns no other `slothealth` caller.

FACT: `applySlotRetryPolicy` is reachable from exactly one production site, `bindSlotWithRetry` (start.go:2736) called from `bindConcurrentSlot`'s `runWithQueue` closure (start.go:2608). The §7.3 resume path does NOT reach it: `resumeOnPod` (start.go:3943) calls `podBinder.Resume` and the handler at :3509 hands any error straight to `holdOrFailOnResumeError` (:3609), which has no retry loop — the FIRST error reaches `isTransientPodClaimError`. — EVIDENCE: pkg/gateway/sessionserver/start.go:2608,:2736,:3509,:3609-3620,:3943.

FACT: the reclaim hold cannot outlive the §10.1.4 pass-2 loop. `onHoldTimeout` runs pass 1 (`deregisterStartedSessions`, slotsession.go:375-396) and then pass 2 serially in the same goroutine under one shared 10s context; `terminateHeldSession` (holdstate.go:228+) defers the per-member release, and a cancelled ctx makes every `Runtime.Close` harsher rather than blocking, so every hold the pass takes is released in the same call. All holds are taken at once and released serially, so a member's identifier can be held up to ~10s — harmless, because slotId == sessionId and those sessions are terminating. — EVIDENCE: pkg/adapter/holdstate.go:189-205,:228-262; pkg/adapter/slotsession.go:375-396.

WATCHOUT: review-log.md:1847 already recorded, as a declined WATCHOUT, that the three callers that can meet the reclaim hold do not run through `applySlotRetryPolicy`. That is the same tree fact my filed finding rests on, but the earlier round read it as "the proposal's sentence is about a path that mostly cannot arise" rather than as the proposal attributing the accounting to the wrong function. The sentence at non-spec-changes.md:1941-1944 is the defect; :1847's reading left it standing.

UNVERIFIED: whether the create-time-reserved §15.1 start retry that meets the hold reaches `accountSlotFailure` at all (CODE-5's reserved branch accounts a `*SlotBindError` from `BindReservedSlot`, but a client-driven re-POST is a fresh request). review-log.md:1847 flags the same gap. Whoever closes the filed finding should settle it in the same edit, because the sentence enumerates all three hold-meeting callers.


### [non-spec-recheck.4.review-reliability.1]

DECISION: returned an empty findings list for the reliability lens on the r4 non-spec delta — BECAUSE every recovery/retry mechanism the delta touches re-derives correctly against the tree, and the reliability-flavoured residues that remain are already carried as Open items (46, 65, 92, 98, 104) or as enumerated accepted failure modes — ALTERNATIVES: rejected filing (a) a tier-8 chaos obligation (declined by four earlier rounds, standing Open 39), (b) the placement-predicate narrowing as a lost protection (it restores shipped behaviour and matches the staged §7.1 predicate exactly), (c) the `PrepareWorkspace` zero-epoch case (unreachable, see FACT below), (d) `SlotBindError.Leaked` meaning two things on two producers (no reader conflates them).

FACT: the snapshot at scratchpad/cp-snap/.../non-spec-recheck-r4 is byte-identical to the working tree, and non-spec-recheck-r3 differs from it only in the review log. The r4 delta is therefore NOT visible from those snapshots: it is the uncommitted working-tree diff against commit c5900230f. Use `git diff proposals/0081_.../` to see it. EVIDENCE: `git status --porcelain proposals/` lists four modified files.

FACT: the r4 delta is four things. (1) the `ExcludePods` placement append narrows from `sbe.Leaked || relErr != nil` to `sbe.Leaked` alone, with a new tier-1 case pinning the two predicates apart (non-spec-changes.md:775-784, :835-841, :875-876, :1552-1557). (2) `resolvePrepareStagingDir` widens to `(string, int64, error)` and `PrepareWorkspace` reports an epoch (:1030-1044, checklist S9). (3) the tier-1 `bindepoch_test.go` cases are split across S9/S10/S11 by the deliverable that makes each pass (:1327-1393, checklist S9/S10/S11). (4) the summary's four 0080 rows are consolidated into one and gain a §1.12 claim-map count (summary.md:659).

FACT: the narrowed placement predicate is correct against the staged spec. CODE-4's compensation returns leaked exactly for `err != nil` and `RECLAIMED && !cleanly` (non-spec-changes.md:557-580), which is verbatim staged §7.1's "A reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit, are the reclaims that did not complete" (spec-changes.md:390). `ReleaseSlotReservation` sends the pod no RPC at all — it is Redis counter plus SandboxClaim only — so the delta's "leaves no residue on the pod" is true. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493-504; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:818-885.

FACT: the delta's claim that every gateway `PrepareWorkspace` call carries at least one upload frame is TRUE on both callers, not just the one it cites. `stageWorkspace` gates on `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323-1330), and the §7.4 mid-session caller, which is NOT so gated (pkg/gateway/sessionserver/upload_to_session.go:128), is fed by `parseUploadToSession`, which 400s on `len(req.Files) == 0` (upload_to_session.go:183-186) and appends one upload per file. `sendUpload` always sends at least one frame even for empty content (pkg/gateway/runtime/adapterclient/client.go:267-285). So standing Opens 73 and 82 ("zero-frame PrepareWorkspace reports zero") are unreachable from the shipped gateway on BOTH paths; the residual is third-party callers only.

FACT: the lazy epoch seed is `time.Now().UnixNano()` (non-spec-changes.md:980-986), which is what makes the fence survive an adapter process restart on a gRPC ClientConn that transparently reconnects: a latch held from before the restart cannot collide with a post-restart mint. Nobody had written this down; it is the reason the "never persisted" property in §4.7.1 is safe rather than dangerous.

FACT: `onHoldTimeout`'s pass 2 shares ONE 10-second `closeCtx` across every member, not 10s per member (pkg/adapter/holdstate.go:200-206). So the reclaim holds CODE-6 has pass 1 take for all members are bounded at ~10s in aggregate, which is what makes §5.2's "the termination window of the pass that runs the cleanup" a real bound rather than an N-multiplied one.

WATCHOUT: `SlotBindError.Leaked` does NOT mean one thing across producers after CODE-4. On the `applySlotRetryPolicy` path it is the pod-side compensation outcome alone; on `BindReservedSlot` and on `Binder.Resume` it additionally folds in the gateway-side release failure (non-spec-changes.md:714-716, the call-site table row for `BindReservedSlot`). That is safe today only because the placement append is the one reader that needs the narrow meaning and it never sees an `sbe` from those two producers. Anything that later routes a reserved-path or resume-path `sbe` into a placement decision breaks silently. EVIDENCE: non-spec-changes.md:775-784 versus :714-716.

UNVERIFIED: whether a `PrepareWorkspace` stream that resolved its entry BEFORE the hold opened can keep writing into `/workspace/slots/{id}/staging` while `removeSlotTree` deletes it, re-creating the tree the cleanup just removed. The hold refuses only at `ensureSlotStateLocked`, and the mid-stream writer never re-resolves (pkg/adapter/staging.go:76-84). This is standing Open 47 seen from the upload side; it is not a regression (today's `removeSlotTree` races the same way with no hold), so I did not file it. Somebody on the adapter code lane should settle it.


### [non-spec-recheck.4.review-security.1]

FACT: THERE IS NO DELTA THIS ROUND. `diff -rq scratchpad/cp-snap/0081_.../non-spec-recheck-r4 proposals/0081_...` returns nothing at all — the snapshot is byte-identical to the working tree, including the review log. The orchestrator's "read the changed sections first" instruction has no target; treat this firing as a full re-read of the staging rather than a delta review. EVIDENCE: `diff -rq` over the two directories, empty output.

FACT: `slotlayout.RemoveTree` really does sweep the credential directory, so CODE-1's widening of the tree removal from `bound` to `removed` is the thing that reclaims `/run/lenny/slots/{sessionId}/credentials.json` for a registered-but-unbound entry. The loop iterates `slotRoot(), Sessions, Artifacts, CredentialsDir`. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69; pkg/adapter/slot.go:210-212.

FACT: the reclaim hold's refusal in `ensureSlotStateLocked` reaches exactly three production call sites and no others — `ensureSlotPaths` (staging.go:134,:181,:337), `claimSessionSlotUnderLock` (slotsession.go:75) and `assignCredentialsSlot` (slotcreds.go:26). Every other session-scoped adapter handler resolves through `slotStateLocked`/`boundSlotState` and therefore answers `FailedPrecondition`/`NotFound` during a hold rather than the `ABORTED` sentinel. That is NOT a contradiction with DOCS-2's "refuses a request that would create or resolve an entry under it": during the hold no entry exists under the identifier, so those requests resolve nothing and the predicate is vacuous for them. This is the same vacuous-resolve argument the log already records against the `Shutdown`-carve-out family; do not re-file it in a credential dress either. EVIDENCE: pkg/adapter/slot.go:105-113; pkg/adapter/slotcreds.go:26,:67-70.

FACT: `DemoteSDK` genuinely removes the registry entry (`anyRegisteredSession` then `releaseSessionSlot`), so CODE-6's clearing of the adapterclient epoch latch on a clean `DemoteSDK` is sound in the fail-closed direction. Had it not removed the entry, clearing the latch would make the next compensation send the UNCONDITIONAL form at a pod that may hold a successor — a fail-open worth filing. Check this again if `DemoteSDK`'s body ever moves. EVIDENCE: pkg/adapter/sdkwarm.go:296-301, :309-316.

FACT: the `Reason()` classifier's `codes.InvalidArgument` arm is NOT stage-scoped (only `FailedPrecondition` is), so the credential stage's `InvalidArgument` wrap really does map to `SlotReasonWorkspaceValidation` and `NonRetryable() == true`. CODE-6's `slotResolveError` helper is load-bearing, not cosmetic. `codes.Aborted` has no case and takes the transient default. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:41-48, :86-102.

FACT: `codes.Aborted` has exactly three production producers in the whole tree today, all on the checkpoint op-lock, so CODE-5's blanket `status.Code(err) == codes.Aborted → true` arm on `isTransientPodClaimError` cannot capture a failure that ought to be terminal. EVIDENCE: `grep -rn "codes.Aborted" pkg/ cmd/ --include=*.go | grep -v _test` → pkg/adapter/oplock.go:37,:82; pkg/adapter/checkpoint.go:115. `isTransientPodClaimError`'s shape matches the proposal's description exactly (typed switch then bare `return false`): pkg/gateway/sessionserver/start.go:3648-3681.

WATCHOUT: CODE-1's staged snippet returns early on the fenced ABSENT/SUPERSEDED arms, which structurally SKIPS the handler's clause three (`if rc := req.GetRecycle(); rc != nil { s.startPodScrub(rc) }`, the whole-pod tenant-residue wipe). I did not file it, on two grounds: no caller can send both (`ShutdownRecycle` passes a zero epoch by CODE-6's own statement, and `ShutdownReclaim` carries no recycle), and if one ever did, the gateway's armed missing-report timeout means the absent `ReportPodScrub` fails the recycle boundary CLOSED rather than handing a dirty pod to the next tenant. A future round that adds a recycle-bearing fenced caller must revisit this. EVIDENCE: pkg/adapter/session.go:284-291; non-spec-changes.md CODE-1's `case !ok:` / `case cur.epoch != epoch:` early returns.

WATCHOUT: the tempting security finding here is "the `leaked` bound is a pod self-report, and SUPERSEDED/ABSENT now evade it whatever `exited_cleanly` says". It does not clear the bar: `exited_cleanly` was ALREADY the self-report driving `leaked` before this amendment (`Binder.ReleaseSlot` sets `leaked = err != nil || !cleanly`), so the trust model is unchanged and the new enum only refines it. The same shape already sits in the log as "MISTAKE nearly filed twice: the withheld report as a residual-state relaxation". Do not re-derive it.

DECISION: returned an empty findings list — BECAUSE every security-lens surface I could reach either fails closed or is unchanged from shipped behaviour: the tree-removal widening strictly increases credential reclamation, the hold is fail-closed on the credential-write path, the epoch clears only where the entry is genuinely gone, the `Aborted` classification has no competing producer, and the drain/usage gates moving from `bound` to `started` withhold signals only for sessions the runtime was never given. ALTERNATIVES: I considered filing (a) the clause-three skip above, (b) a missing tier-9 arm for the hold-refuses-`AssignCredentials` ordering, and (c) the `ExcludePods` suppression on SUPERSEDED. (b) is already pinned deterministically at tier 1 in CODE-6's hold case and would be nice-to-have coverage; (c) is correct by construction, since on SUPERSEDED and ABSENT the adapter performed nothing and on RECLAIMED the cleanup finished before the answer.


### [non-spec-recheck.4.review-test-coverage.1]

FACT: THE r4 SNAPSHOT IS AGAIN USELESS. `scratchpad/cp-snap/0081_.../non-spec-recheck-r4`,
`-r4-start` and `-r3` are all byte-identical to the live proposal directory; the only file that
differs against `-r2-prefix` is non-spec-changes.md (two hunks, both already reviewed by r3).
So round 4 had no delta at all and is a full re-read under the lens.
EVIDENCE: `diff -rq scratchpad/cp-snap/0081_*/non-spec-recheck-r4 proposals/0081_*` is empty.

FACT: the three `staging.go` resolve sites that CODE-6 routes through `slotResolveCategory`
stamp `tracing.CategoryPermanent` in the shipped tree, at pkg/adapter/staging.go:81, :183 and
:339. CODE-6's helper exists to answer `CategoryTransient` for the reclaim-hold sentinel at
exactly those three sites. The tier-1 "refusal stays retryable through all five resolve sites"
bullet (non-spec-changes.md:1380-1386) asserts the span category only for the NON-sentinel arm,
so nothing turns red if an implementor leaves the three `CategoryPermanent` stamps in place.
Filed as a missing-test finding.

FACT: "refuses" is now overloaded in this proposal. After the amendment it names the
`SUPERSEDED` answer, which CODE-4 maps to `leaked=false` (non-spec-changes.md:568-575) and
which CODE-5 explicitly excludes from the `ExcludePods` placement filter
(non-spec-changes.md:958-960, "the exclusion keeps a retry off a pod whose reclaim went
unanswered at all"). The tier-4 datastore-crossing case at non-spec-changes.md:1713-1723 still
uses the pre-amendment sense ("a bind whose compensating `Shutdown` the adapter refuses is
released with `leaked=true`" and "the pod whose reclaim was refused"), which is stale text the
amendment did not sweep. Verified pre-amendment at
`git show 2811d55fe:proposals/0081_.../...non-spec-changes.md:973`. WHOEVER FIXES IT should
sweep the whole Testing section for "refuse"/"refused" used where "did not answer" is meant.

WATCHOUT: the per-stage compensation-table assertion (non-spec-changes.md:1516-1522) asserts
`deadlineMs` "equal to half the budget the case's pool configuration produces" — a
self-referential relation computed from `slotCleanupBudget` itself. It cannot catch a wrong
budget, so it does NOT double as a test of the `max(cleanupTimeoutSeconds/maxConcurrentSessions,
5)` formula or its floor. Do not cite it as covering the floor.
EVIDENCE: non-spec-changes.md:508-516, :1516-1522

UNVERIFIED: SPEC-2's replaced §7.2 step 3 (spec-changes.md:456) puts a new gateway obligation on
the mid-resume terminal-collapse path ("the gateway sends [the reclaim] on the connection that
attempt still holds, before the half-claimed replacement pod is released"). No CODE deliverable
names that path and no listed test carries a `// spec: §7.2`. It may be discharged by CODE-4's
`Binder.Resume` compensation by reference, but nobody has shown that the DELETE-during-`resuming`
handler (pkg/gateway/sessionserver/sessionserver.go:2950-2958, :2995-3008) actually routes
through a failing `Binder.Resume`. An edit-site or mechanism lens should settle this; I did not
file it because the answer turns on code paths outside my lens.


### [non-spec-recheck.5.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens — BECAUSE every claim in the r4→r5 delta verified against the tree, and the checklist simulates cleanly (14 steps, one lane each, spec block leads, every deliverable in exactly one step, no Depends-on naming a later or missing step, no checked box, no forward reference) — ALTERNATIVES: I considered filing the claim-register `surface` string mismatch (`Server.nextBindEpoch` at non-spec-changes.md:1246 vs CODE-6's `nextBindEpochLocked` at :977) and CODE-6's heading/summary file lists omitting `pkg/adapter/resume.go` and `pkg/adapter/session.go` while CODE-6 stages the `ResumeResponse`/`StartSessionResponse` epoch stamps; both are edit-list/naming granularity that `## Files touched on application` already covers, and this loop has refuted that class repeatedly.

FACT: the r5 delta is exactly five things — `tracing.Category` → `tracing.ErrorCategory`; the span-`error.category` assertions added to the five-resolve-site tier-1 bullet; the tier-4 case switching from "reclaim refused" to "reclaim unanswered" driven by a client interceptor on `recycleAdapterDialer`; the `slotAddressCaseFiles` credit-inventory edits for the two new `slot*_test.go` files; and the matching `## Files touched` bullet + S10 checklist sentence. Locate it with `diff -u cp-snap/.../non-spec-recheck-r4-prefix cp-snap/.../non-spec-recheck-r5` — the `non-spec-recheck-r5` snapshot is byte-identical to the live proposal, so diffing against it yields nothing.

FACT: every anchor the delta cites resolves. `tracing.ErrorCategory` pkg/observability/tracing/tracing.go:85; `AttrErrorCategory` :107; `RecordError` sets it at :210 (the cited :202 is inside that function's doc block, close enough to be the same site). `installInternalSpanRecorder` pkg/adapter/tracing_internal_test.go:23, `endedSpanNamed` :33. The three literal `tracing.CategorizeError(err, tracing.CategoryPermanent)` stamps are at pkg/adapter/staging.go:81 (PrepareWorkspace/resolvePrepareStagingDir), :183 (FinalizeWorkspace), :339 (RunSetup), and all three handlers do wire `spanErr` into a deferred `tracing.RecordError` (:36-41, :163, :320). `adapterclient.Dial` is variadic over `grpc.DialOption` at pkg/gateway/runtime/adapterclient/client.go:48 and `recycleAdapterDialer` calls it at tests/tier4_integration/recycle_scrub_path_test.go:213.

FACT: the credit-inventory gate the delta answers is real and stricter than the delta's paraphrase. `slotSubjectFileRE` is `(slot|one_session_only|sole_session)[^/]*_test\.go$` and `slotSurfaceCallRE` is `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`, both at tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173, walked over cmd/migrations/pkg/scripts/sdks/tests (:1191). Adding a file to `slotAddressCaseFiles` also subjects it to `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:970) and `TestAddressCaseNamesAgreeWithTheirOwnCitations` (:1022) — the proposal's `// spec: §5.2` + name `..._spec_5_2` + spec-map entry under 5.2 satisfies both, as does the tier-9 file's `§4.7; §4.9; §5.2` against its 4.7/4.9/5.2 entry.

FACT: none of the other new test surfaces trips the inventory gate. `pkg/adapter/bindepoch_test.go`, `tests/tier10_conformance/bind_epoch_conformance_test.go` and the `tests/tier3_contract/adapter_bind_epoch/` files match neither regex — note `claimSessionSlot(`/`releaseSessionSlot(` do NOT contain `ClaimSlot(`/`ReleaseSlot(`, so adapter-side helpers are invisible to the surface rule. The exposed files the proposal edits that are NOT currently in the inventory are `pkg/adapter/socketruntime_test.go`, `pkg/gateway/sessionserver/start_test.go` and `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`; all three currently score 0 on the surface regex, so they stay clean unless an implementor writes a fake method literally named `BindReservedSlot(`. The proposal's blanket rule at non-spec-changes.md:2060-2068 covers that case.

FACT: all nine SCHEMA-1 field numbers are free, checked by dumping each message from schemas/lenny-adapter.proto — `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (:1618), `AssignCredentialsResponse` is `{}`, `ResumeResponse` holds 1-3, the rest hold 1 or 1-2. `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is at tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208 with `assertFieldSet` at :259 and its unexpected-field error at :264-267, exactly as SCHEMA-1 describes. Sections 4.7, 4.7.1, 4.9, 5.2, 6.2, 7.1 and 15.4 all already exist as keys in tests/spec-map.json, so no new section key has to be minted, and the `dir/...` entry form is already in use (tests/spec-map.json:1997).

WATCHOUT: the claim-register gates do NOT resolve a `surface` symbol — tests/tier0_static/claim_register_test.go:275,:278 only check that a WIRED row names something that is not a bare line number. So the `Server.nextBindEpoch` / `nextBindEpochLocked` mismatch will not turn any gate red; it is a documentation-grade defect that a future filing should not present as a gate failure.

UNVERIFIED: whether the tier-4 interceptor can be added without perturbing the six other `recycleAdapterDialer` call sites (tests/tier4_integration/recycle_scrub_path_test.go:409,:512,:570,:676,:725,:797). The proposal implies a per-case variant dialer but does not say so; an implementor who edits the shared helper in place breaks the other six cases. Someone running the implementation should check this.


### [non-spec-recheck.5.review-test-coverage.1]

DECISION: returned an empty findings list for the test-coverage lens on the r4→r5 delta and the whole staging — BECAUSE every delta claim I could check resolves against the tree, and every behavior the proposal changes has a concrete listed test at a tier it actually reaches — ALTERNATIVES: I considered filing (a) the `ConfigureWorkspace` idempotent-repeat epoch arm as unpinned, (b) the `DemoteSDK` DOCS-2 row as having no tier-11 substring assertion, and (c) the tier-4 datastore-crossing case asserting `RecordLeak`/gauge/re-bind that only `pkg/gateway/sessionserver` produces. All three fell below the bar: (a) is not a separate branch once `claimSessionSlot` widens to return the epoch, (b) is doc-row granularity, (c) is the same fixture-capability family already refuted twice and the behavior is pinned at tier 1 anyway.

FACT: every citation the r5 delta added verifies. `tracing.ErrorCategory` is the real type name (the r4 text said `tracing.Category`, which does not exist) — EVIDENCE: pkg/observability/tracing/tracing.go:85. `AttrErrorCategory = "error.category"` at :107 and it is set in `RecordError` at :210 (the proposal cites ":202", off by eight but the function body is :202-213, so the citation is to the function). `installInternalSpanRecorder` is at pkg/adapter/tracing_internal_test.go:23 and `endedSpanNamed` at :33, exactly as cited. The three `tracing.CategorizeError(perr|derr, tracing.CategoryPermanent)` resolve-site stamps are at pkg/adapter/staging.go:81, :183, :339, exactly as cited, and all three handlers do `defer tracing.RecordError(span, spanErr)` (staging.go:39, :166, :323), so the span-category assertion the delta adds is writable.

FACT: the tier-0 credit-inventory gate the delta now names is real and works the way the delta describes. `slotAddressCaseFiles` is at tests/tier0_static/spec_map_slot_address_registration_test.go:236; the completeness gate is `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` at :1108, fed by `derivedInventoryCaseFiles` at :1213; the two derived rules are `slotSurfaceCallRE` (`slotstate.|ClaimSlot(|ReleaseSlot(|ReserveSlotOnPod(|claimAtCreate(|BindReservedSlot(`) at :1168 and `slotSubjectFileRE` (`(slot|one_session_only|sole_session)[^/]*_test\.go$`) at :1174. Both new slot-named files match rule 2, and the delta puts both into the inventory at S10 in the checklist and in `## Files touched`.

FACT: the other new test files this proposal creates do NOT need an inventory entry, and I checked rather than assumed. `pkg/adapter/bindepoch_test.go` and `tests/tier10_conformance/bind_epoch_conformance_test.go` match neither derived rule (no `slot` in the path, and the regex is case-sensitive so adapter-internal `releaseSessionSlot(` / `claimSessionSlot(` do not match `ReleaseSlot(` / `ClaimSlot(`). `pkg/adapter/socketruntime_test.go`, which the tier-1 co-tenancy case edits, is likewise outside both rules and outside the inventory today — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:279 lists only `socketruntime_e2e_test.go`.

WATCHOUT: the one inventory hole the delta leaves is contingent and unfileable as written. The new tier-3 directory `tests/tier3_contract/adapter_bind_epoch/` has no file name stated anywhere in the proposal, so if an implementor names its file `slot_*_test.go` it must enter `slotAddressCaseFiles` at S9 and nothing in the proposal says so. Do not file this without first establishing that the proposal fixes the name — as it stands it is an implementor choice that happens to be unbounded, and the bounded reading (any non-`slot`-named file) is fine.

FACT: the credit gate `TestEveryAddressCaseIsCreditedToItsOwnCitedSections` (:970) requires spec-map to credit each inventoried file to every section its `// spec:` annotation names. The two new files clear it as staged: tier-7a carries `// spec: §5.2` and gets a 5.2 map entry; tier-9 carries `// spec: §4.7; §4.9; §5.2` and gets 4.7/4.9/5.2. A future fixer who widens either annotation must widen the map entry in the same step or tier 0 goes red.

FACT: the r5 tier-4 rewrite is mechanically sound. `recycleAdapterDialer` (tests/tier4_integration/recycle_scrub_path_test.go:206) does go through `adapterclient.Dial`, which is variadic over `grpc.DialOption` at pkg/gateway/runtime/adapterclient/client.go:48 exactly as cited, so a unary client interceptor is injectable. And an unanswered `Shutdown` does yield `leaked=true` under CODE-4's new mapping (the `err != nil` arm), which is what the r4 text's "refuses" trigger did not.

USEFUL [Standing context: "The `leaked` disposition already exists end to end"] and [Settled: "`SlotClaimer.ReleaseSlot(leaked=true)` returns early"]: these two let me confirm the tier-4 datastore-crossing assertions (Redis counter not decremented, claim survives at `bound`) without re-deriving the release path.

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

### [f1.open-decisions.human-decisions.15]

DECISION: entry 15 ("does §7.1's exclusive-pod clause need a mid-resume carve-out?") is resolved
as no carve-out and deleted from `## Open decisions for human to make` in the summary. The
resolution is recorded in that section's preamble paragraph, beside the note that already records
entries 12, 13 and 14 leaving. No staged spec or non-spec text changed, because the answer is that
the staging is already correct: §7.1 ends at spec-changes.md:392 with "The pre-attached disposition
governs the pod; this reclaim governs the slot state on a pod that is released or reused rather
than terminated", and SPEC-2's §7.2 step 3 states no pod outcome by design.

FACT: the mid-resume pod's disposition is enumerated rather than unsettled.
`spec/06_warm-pod-model.md:229` declares the `resuming` failure-transitions subsection the
"**authoritative enumeration** of every edge out of the session-model `resuming` state", and
`:234` states the outcome for that exact pod, "the half-claimed replacement pod is released to the
pool". CORRECTS the item design, which cited the authoritative-enumeration sentence at
`spec/06_warm-pod-model.md:230`; it is at `:229`. The `:234` citation is exact.

FACT: the residue on a released half-claimed pod is disposed of in both pool configurations by
shipped text. Non-recycling: `spec/06_warm-pod-model.md:80` projects "a claim deleted on a pod with
`recycle.enabled: false`" through `draining` and then `terminated`. Recycling:
`spec/05_runtime-registry-and-pool-model.md:453` states "a whole-pod scrub runs whenever occupancy
reaches zero on a recycling pod before the pod is reused", and
`pkg/gateway/podlifecycle/podsession/binder.go:498-501` records that trigger on the release path.
CORRECTS the item design's `binder.go:497-505`; the sentence is the `Recycle` field doc comment at
`:498-501`.

FACT: no staged change file carries an `## Open decisions for review` section, confirmed again at
this firing, so entry 15 had nothing to delete outside the summary.

WATCHOUT: the preamble sentence "entries 15 through 18 were carried out of the review log by the
index-and-checklist reconciliation pass" was falsified by this deletion and now reads "entries 16
through 18". A later firing that resolves 16, 17 or 18 owes the same reconciliation.

No staged deliverable is added, removed, merged, split or resequenced, so the implementation
checklist is unchanged.

### [f1.open-decisions.out-of-scope-defects.threshold-retune]

DECISION: retuning the `ceil(maxConcurrentSessions/2)` unhealthy threshold stays out of scope and
`## Defects in the shipped tree that this proposal does not stage` gains no row for it. I wrote
nothing into the proposal, because it already carries the item exactly as adjudicated: the
Non-goals bullet stands verbatim at `0081_....summary.md:315-316`, the defects section
(`0081_....summary.md:426-651`, thirteen bullets) carries no threshold row, and the problem
statement's NAMED OUT OF SCOPE paragraph names the retuning at
`0081_....problem-statement.md:141-142`.
FACT: the behavioural cost is recorded where it belongs, as the "Faster pod churn at
`maxConcurrentSessions >= 3`" bullet under "Watch out for" at
`0081_....non-spec-changes.md:1954-1963`, whose closing sentences state that the §7.3 re-attach
reaches the accounting for the first time and so drains a replacement pod at
`maxConcurrentSessions: 2`. This CORRECTS the `:1764-1773` citation in the earlier
`[f1.open-decisions.threshold-retune]` block, which has drifted; the bullet's content is unchanged.
FACT: no staged deliverable touches the formula, its denominator, or the drain block.
`UnhealthyThreshold` is at `pkg/gateway/runtime/slothealth/slothealth.go:215`, `Tracker.Unhealthy`
at `:136`, and the sole production trigger at `pkg/gateway/sessionserver/start.go:2858`. CODE-5
adds callers that reach that trigger and moves nothing. These CORRECT the `:214-220`/`:136-140`
and `:215-220`/`:2857` citations in the two earlier blocks by one to two lines.
FACT: the shipped code conforms to the spec here, so a defects row would have no divergence to
cite. `spec/06_warm-pod-model.md:160` states the rolling-window failures plus persistent leaks
reaching `ceil(maxConcurrentSessions/2)`, which is what the clamped `(maxConcurrent+1)/2` computes.
Every row the defects section carries is a spec-versus-tree divergence; a conformant tuning value
is not one.

No staged deliverable is added, removed, merged, split or resequenced, so the implementation
checklist is unchanged.

### [f1.summary-cleanup]

FACT: the summary already carried exactly the eight required sections, in order, so this format
pass rewrote nothing. Headings as they stand: `# Summary: ...` (:1), `## Summary` (:3) holding
`**Problem statement.**` (:5), `**What changes.**` (:21), `**Decisions.**` (:86) and
`**Watch out for.**` (:169); `## Goals` (:219); `## Non-goals` (:231); `## Open decisions for
human to make` (:328); `## Defects in the shipped tree that this proposal does not stage` (:426);
`## Impacts on other proposals` (:655); `## Deliverable index` (:669), last and untouched.

FACT: the parts already carry their current names. No `**Fixed decisions.**` or `**What is
fixed.**` label survives, so no rename was owed.

FACT: `## Open decisions for human to make` carries entries 11, 16, 17 and 18, which is exactly
the set this firing leaves with the human: 11, 16 and 18 had their proposed resolutions refuted at
the gate and at apply, and 17 was adjudicated as the human's with the summary already carrying it
as written. Entry 15, resolved and applied at this firing, had already left the section, and the
preamble records its departure with the ground. Every identifier is preserved verbatim.

FACT: no `### Retired` block, no meta-list of staged items with dispositions, and no errata block
of corrections owed to files this loop cannot edit exists anywhere in the file, so nothing was
relocated and nothing was left unplaceable.

FACT: the section preamble at `:330-356` was re-read against the four entries the section now
carries and stands true as written. Its statement that entry 15's mid-resume carve-out is owned
elsewhere re-verified against the tree: `spec/06_warm-pod-model.md` "`resuming` failure
transitions" is self-declared authoritative over every edge out of `resuming` and states that the
half-claimed replacement pod is released to the pool, and §5.2's "Scrub model" paragraph states
that a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is
reused.

FACT: the thirteen defect bullets under `## Defects in the shipped tree that this proposal does
not stage` are unchanged and none of them is a threshold row, which is what the
`[f1.open-decisions.out-of-scope-defects.threshold-retune]` reading required.

WATCHOUT: `## Impacts on other proposals` carries four rows for proposal 0080, one per inventory
section (§1.2, §1.19, §1.7, and the grouped §1.1/§1.3/§1.4/§1.5/§1.16/§1.20 row). The section's
own rule is one row per proposal. Merging them is a rewrite rather than a move, so this pass left
them; a later pass that consolidates them must carry all four assessments into the merged row.

WATCHOUT: the SCHEMA-1 window under `**Decisions.**` and the remediation step R1b row under
`## Impacts on other proposals` both assert the S-2 second-window precondition. They agree today.
The decisions bullet carries it as this proposal's own step-ordering constraint and the impacts
row carries the assertion about the programme step, so neither was folded into the other; a later
edit to one owes the same edit to the other.

No staged deliverable is added, removed, merged, split or resequenced, so the implementation
checklist is unchanged.

### [spec-recheck.2.fix-slot-substate-fence]

These are corrections to the same pass that reworded the `running` boundary, appended here
because that pass left no subsection of its own in this file.

- **The reworded boundary contradicted "a start still in flight" inside its own paragraph.**
  The pass had set sentence 1 of SPEC-4's staged §6.2 prose to "dispatched to the pod's
  shared runtime process with its session identifier, which is the moment the
  `receiving_uploads → running` trigger above names", while the next clause still placed a
  start still in flight, dispatched and not yet arrived, in `receiving_uploads`. The two
  sentences named two different moments. The boundary is now stated once, at arrival: "A
  slot reaches `running` when the pod's shared runtime process has been given the session
  with its session identifier." The clause attributing that moment to the fence's trigger is
  deleted, so the paragraph states the boundary on its own authority and no longer glosses
  the trigger's words. The same substitution is applied to the Design section's parallel
  sentence.
- **The four parallel statements the pass did not reconcile now agree with the edited two.**
  Re-read after the edit: the §4.7 `Shutdown` row's "the slot's `running` sub-state and the
  cleanup-outcome report take the later moment, when the pod's shared runtime process has
  been given the session"; SPEC-3's withheld-report rule, keyed to "the sessions the pod's
  shared runtime process has been given"; SPEC-4's staged edge annotation, "before the
  runtime has been given the session, a start still in flight included"; and the edited
  paragraph's own later sentence, "a session the runtime has already been given is a slot in
  `running`". All four take arrival as the boundary, which is what sentence 1 now takes. The
  code and docs lanes already read arrival (`non-spec-changes.md:485` and `:1249`), so this
  edit falsified nothing there and that file was not opened.
- **What the edit deliberately does not do.** It does not touch the shipped
  `receiving_uploads ──→ running` trigger annotation in `spec/06_warm-pod-model.md`, whose
  words are "workspace ready, session dispatched to runtime with its session identifier".
  Rewording that annotation is the alternative remedy, and it would also owe an edit to
  `docs/reference/state-machines.md`, a docs-lane deliverable this loop may not author. The
  standing reading, that a start still in flight sits between the adapter's admission of the
  start RPC and the runtime being given the session, is unchanged and is what the paragraph
  now states directly.
- DEFERRED: whether SPEC-4's fence annotation should be reworded to the arrival moment, with
  `docs/reference/state-machines.md`'s mirrored row added to DOCS-1's edit list. That is the
  option-(b) remedy and it crosses into the docs lane, so it is left for the pass between the
  loops.

### [f2.open-decisions.0080-consolidated]

Item: the `impact-row` disposition on proposal 0080's §1.2, held for firing 2 (post-spec-recheck).

- DECISION: the four separate 0080 rows in `summary.md`'s `## Impacts on other proposals` are
  consolidated into one row, per that section's one-row-per-proposal rule. Every assessment the
  four rows carried is kept verbatim inside the consolidated cell, under a bolded sub-label per
  inventory entry (`**§1.2 ...**`, `**§1.12 ...**`, `**§1.19 ...**`, `**§1.7 ...**`, and the
  file-collision group). The four "What it must do" cells are merged into one instruction to
  re-derive four entries when 0080 is triaged.
- FACT: 0080's status is Draft and it stages nothing. Its own `Date` line reads 2026-08-31
  (`proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:8`) and
  `git log -1` on the file gives 2026-09-06 (`a5476f93e`). The row now names both and says the
  last-commit date records a touch rather than a review.
- FACT: §1.2 is discharged in its entry-removal half only. Its third clause, that a later session
  on the pod arms neither MCP server, has a surviving independent cause:
  `claimSessionSlotUnderLock` inserts the claimant's own entry through `ensureSlotStateLocked`
  (`pkg/adapter/slotsession.go:75`) and then gates the arming on `len(s.slots) != 1`
  (`:106-110`), so two ordinary interleaved binds miss the arming with no failed bind anywhere.
  Re-derived directly this firing. That clause is already recorded under
  `## Defects in the shipped tree that this proposal does not stage` (`summary.md:428-437`), so
  the row points at it rather than restating it.
- FACT: the reclaim hold adds no fourth class to §1.19. `CoordinatorFence` resolves through
  `s.boundSlotState(sessionID)` (`pkg/adapter/coordination.go:116`) and `CheckpointBarrier`
  through the same call (`:254`); neither reaches `ensureSlotStateLocked`, where
  `errSlotReclaimInProgress` raises its `codes.Aborted`. The hold's refusal carries neither
  §1.19's status code nor its RPC set. The item's own flip condition therefore does not fire.
- FACT: §1.12's arithmetic moves. `tests/claim-map.json` holds 76 rows today, counted this
  firing: 20 `ABSENT`, 24 `UNWIRED`, 32 `WIRED`. SCHEMA-1 adds two `WIRED` rows
  (`non-spec-changes.md:1193-1224, :1989-1992`), so the denominator becomes seventy-eight while
  the twenty `ABSENT` rows, the two owned rows and the eighteen with no owner stand, and §1.18's
  twenty-four `UNWIRED` rows are untouched. This is the one assessment the four old rows did not
  carry.
- FACT: the §1.7 and file-collision assessments are carried across unchanged. Their text was
  moved by script rather than retyped, so no citation in either was altered.
- WATCHOUT: the compaction pass 11 changelog records the older "nine rows with no duplicate
  subject" reading of this section. That count is now eight rows, because the four 0080 rows
  became one.
- No staged deliverable is added, removed, merged, split or resequenced, so the implementation
  checklist is unchanged.

### [f2.summary-cleanup]

FACT: the summary already carried exactly the eight required sections, in order, so this format
pass rewrote nothing. Headings as they stand: `# Summary: ...` (:1); `## Summary` (:3) holding
`**Problem statement.**` (:5), `**What changes.**` (:21), `**Decisions.**` (:86) and
`**Watch out for.**` (:169); `## Goals` (:219); `## Non-goals` (:231); `## Open decisions for
human to make` (:328); `## Defects in the shipped tree that this proposal does not stage` (:426);
`## Impacts on other proposals` (:655); `## Deliverable index` (:666), last and untouched.

FACT: the parts carry their current names already. No `**Fixed decisions.**` and no `**What is
fixed.**` label survives, so no rename was owed and `**Problem statement.**` was not touched.

FACT: `## Open decisions for human to make` carries entries 11, 16, 17 and 18, which is the set
this firing leaves with the human. Items 11, 16 and 18 were adjudicated `resolve`, refuted at the
gate, and not attempted at apply, so nothing was staged for them and each stays the human's. Item
17 was adjudicated the human's with the summary already carrying it as written. Every identifier
is preserved verbatim and none was renumbered.

FACT: the section preamble at `:330-356` was re-read against the four entries the section now
carries and stands true. Its numbering claim holds: the list starts at 11 and skips 12 through 15.
Its entry-15 ground was re-verified against the tree this firing: `spec/06_warm-pod-model.md`
"`resuming` failure transitions" declares itself the authoritative enumeration of every edge out
of `resuming` and states that the half-claimed replacement pod is released to the pool; §6.2's
`Sandbox.status.phase` paragraph states that a claim deleted on a pod with `recycle.enabled:
false` projects `draining` and then `terminated`; §5.2's "Scrub model" paragraph states that a
whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused;
and `pkg/gateway/podlifecycle/podsession/binder.go` signals that scrub on the release path.

FACT: no `### Retired` block, no meta-list of staged items with dispositions, and no errata block
of corrections owed to files this loop cannot edit exists anywhere in the file, so nothing was
relocated, nothing was deferred, and nothing was left unplaceable.

FACT: the thirteen defect bullets under `## Defects in the shipped tree that this proposal does
not stage` are unchanged, and the section carries no decision.

FACT: `## Impacts on other proposals` now carries six rows, one per proposal or programme step
(0080, 0073, 0075, 0078, R1b, R12). The four separate 0080 rows the previous firing recorded as a
duplicate subject were consolidated by this firing's write path, so that watch-out is discharged.

WATCHOUT: the SCHEMA-1 bullet under `**Decisions.**` and the R1b row under `## Impacts on other
proposals` both assert rule S-2's second-window precondition. They agree today. The decisions
bullet carries it as this proposal's own step-ordering constraint and the impacts row carries the
assertion about the programme step, so neither was folded into the other; an edit to one owes the
same edit to the other.

WATCHOUT: `## Non-goals` and two defect bullets name proposals 0078, 0079 and remediation step
R12 inside entries whose subject is this proposal's own scope. They agree with the matching rows
in `## Impacts on other proposals`. They were left in place because lifting a clause out of a
non-goal or a defect entry would gut the entry rather than move a block of prose about another
proposal.

No staged deliverable is added, removed, merged, split or resequenced, so the implementation
checklist is unchanged.

### [non-spec-recheck.1.fix-epoch-plumbing]

CORRECTIONS to the epoch-plumbing and test-step-assignment pass of non-spec-recheck round 1,
from the post-fix review of that pass's own edits. The pass left no shard of its own in this
file, so its corrections open one here rather than a pass of their own.

- The `PrepareWorkspace` plumbing paragraph the pass added closed with "A stream that carries
  no upload frame resolves nothing, touches no entry, and reports zero", which the staged
  §4.7.1 text contradicts. SPEC-5 states the rule on the response rather than on the plan
  ("Each of them reports the entry's current epoch on its response") and its §15.4
  non-conformance clause lists "reports zero on a bind-sequence response" with no upload-free
  exception, and CODE-6's own `ConfigureWorkspace` sentence applies the same rule to the
  parallel idempotent-repeat arm. The two sites the sentence cited as already stating its
  claim state something else: SCHEMA-1's "required" marking and the tier-3 case fix which
  response is the first epoch-bearing one on an upload-free plan. The tree settles the
  question rather than the spec having to carve an exception: `stageWorkspace` calls
  `PrepareWorkspace` only inside `if len(uploads) > 0`
  (`pkg/gateway/podlifecycle/podsession/binder.go:1322-1327`) and the client sends a frame per
  upload (`pkg/gateway/runtime/adapterclient/client.go:247-266`), so every call the gateway
  makes carries at least one upload frame. The paragraph now says that, and no staged
  response reports zero. No spec-changes edit is needed and none was made.
- The S9 and S10 checklist lines the pass wrote enumerated different sets for the same
  deferred tier-1 cases: S9 deferred "the `Shutdown`-outcome and start-confirmation cases" to
  CODE-1 and CODE-2, while S10 claimed "the `Shutdown`-outcome and hold cases". Three bullets
  in the tier-1 list carry an **S10.** prefix, one outcome case and two `Shutdown`-hold cases,
  so on S9's line the two hold cases read as S9's own work. S9 now names the same split S10
  does: the `Shutdown`-outcome and `Shutdown`-hold cases land with CODE-1 and the
  start-confirmation cases with CODE-2.

No staged deliverable is added, removed, merged, split or resequenced by either correction, so
the implementation checklist keeps its fourteen steps and every box stays unticked.
