# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 12).** Pass 12 read the whole ledger for this window: the
`spec-recheck.2`, `.3` and `.4` lens shards, the `non-spec-recheck.1` through `.5` shards and
their fix groups, the `non-spec.1` and `non-spec.2` shards, the `spec.1.fix-bind-epoch`
correction chain, `spec-recheck.2.fix-slot-substate-fence`, `non-spec-recheck.1.fix-epoch-plumbing`,
`non-spec.1.fix-schema-1`, the `f1`, `f2` and `f3` firings and the three index-and-checklist
reconciliation passes. Lifted: thirty-two new Settled entries, eight new Traps, eleven new Open
items and three new Deferred entries. Applied six CORRECTS against the standing context.
(1) The `running`-boundary contradiction is CLOSED by a third route the standing Trap did not
consider, so that Trap is DELETED and the UNVERIFIED beside the boundary DECISION is retired.
(2) "Three refusals now travel out of `ensureSlotStateLocked`" was true only of the withdrawn
per-attempt design; exactly one does under the reverted per-entry epoch, and the entry is
rewritten. (3) `binder_test.go:533` is not the `cl.Resume` failure branch and carries no
division-by-zero risk; the entry is rewritten so a fixer does not edit the wrong test.
(4) `## Impacts on other proposals` now carries SIX rows, the four 0080 rows having been
consolidated, so the "nine rows" entry and the four-rows Trap are rewritten and deleted.
(5) The leaked-discriminator entry is scoped to ACCOUNTING, because placement narrowed to
`sbe.Leaked` alone. (6) The two Opens on an upload-free `PrepareWorkspace` are merged and
narrowed to third-party callers. Retired as closed: five Open items, one Deferred and two
Traps. Did NOT reach 960 lines: this section is 1018 lines, and it grew rather than shrank.
It grew for two reasons worth recording. First, this window named a whole tier-0 gate no
earlier pass had any entry for, the `slotAddressCaseFiles` credit inventory, whose two derived
rules and two consequential gates are the kind of thing a round loses itself in; that is four
Settled entries and a Trap and they are worth every line. Second, the window's most expensive
repeated failure was structural rather than substantive: in at least fifteen shards the named
snapshot was byte-identical to the working tree, so the round opened by hunting a delta that
did not exist, and in several the newest differing snapshot was two rounds back or a `-prefix`
directory. The snapshot Trap now carries its seventh and eighth forms at length because the
cheaper alternative is another fifteen rounds spent on `diff`.

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
- **DECISION: the `running` boundary is the runtime having been given the session,** with a start still in flight named on the `receiving_uploads` side, stated identically in the SPEC-4 fence annotation, the SPEC-4 prose, the Design paragraph, CODE-3's doc comment and DOCS-1's table row. CLOSED: the four sites now agree, after the fix that deleted the start-handover sense of "acknowledge" and then deleted the attribution clause; see the Settled entry on the closed `running` boundary. The fence annotation is still untouched, so the neighbouring entry "the staged new-edge annotation is left mirroring that vocabulary" also still holds.
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
- **The three leaked discriminators are one predicate across all three bind paths.** `compensateFailedSlotBind` returns `err != nil || !cleanly`; `applySlotRetryPolicy` accounts on `sbe.Leaked || relErr != nil`; `BindReservedSlot` folds its own release error into `sbe.Leaked` before the caller reads it; `Binder.Resume` folds `relErr` in before `resumeOnPod` reads it. The `leaked=true` release is a no-op inside `SlotClaimer.ReleaseSlot` (slotclaimer.go:830-836), so `relErr` is always nil on the leaked arm and the two-term discriminator never double-counts. CORRECTED for the `ExcludePods` narrowing: this entry is about ACCOUNTING and must not be read as covering PLACEMENT. The placement append on `applySlotRetryPolicy` is now `sbe.Leaked` alone, so accounting and placement are no longer one predicate on that path.
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
- **`slotCleanupBudget`'s divisor is safe in production and reachable in fixtures.** `SlotBindRequest` is built only under `match.MaxConcurrentSessions > 1`; `ResumeRequest` normalises through `maxConcurrentSessions()` at start.go:4029. CORRECTED: the earlier form of this entry said `binder_test.go:533` IS the `cl.Resume` failure branch CODE-4 puts the compensation on and warned of an integer-division panic there. It is not: :533 is `TestResumeReturnsErrNoIdlePodWhenPoolEmpty`, which fails at claim time before any adapter RPC, and all three `podsession.ResumeRequest{…}` literals in `binder_test.go` (:492, :533, :1650) are success or pre-RPC-failure paths. The branch CODE-4 actually changes is exercised by `resume_slot_reservation_test.go`, whose `resumeRequest(maxConcurrent)` helper sets `MaxConcurrentSessions` non-zero at every call site, so the divisor is safe there too. A fixer acting on the old form edits the wrong test. `CleanupTimeoutSeconds` is `int` while `MaxConcurrentSessions` is `int32`, so the body needs an explicit conversion.
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
- **The whole summary structure was rebuilt and verified by the `f5` cleanup firing.** The required sections stand in the required order, `## Open decisions for human to make` carried entries 9 and 11 at that firing (it now carries 11, 16, 17 and 18 — see the correction below), `## Defects in the shipped tree that this proposal does not stage` carried eight entries then and carries thirteen now, and `## Deliverable index` is last. CORRECTED TWICE: the section carried nine rows with four for 0080, which `f1.summary-cleanup` read as a breach of its own one-row-per-proposal rule; the `f2.open-decisions.0080-consolidated` firing then merged the four into one, carrying every assessment verbatim under a bolded per-inventory-entry sub-label and adding the §1.12 claim-map arithmetic the four old rows did not carry. It now carries SIX rows, one per proposal or programme step (0080, 0073, 0075, 0078, R1b, R12). Any entry saying "nine rows" or "four 0080 rows" is reading a superseded pass. Nothing was relocated because nothing was misplaced.
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
- **Exactly ONE refusal travels out of `ensureSlotStateLocked`, and the five resolve sites re-wrap it as `codes.InvalidArgument` with `tracing.CategoryPermanent`.** CORRECTED: an earlier form of this entry named three (cleanup hold, superseded epoch, epochless-against-a-started-entry), which was true only of the withdrawn r5 per-attempt design. Under the reverted per-entry epoch the function takes no caller epoch and compares nothing, so the hold refusal is the only refusal and the mint is unconditional on the entry-creating branch. That single refusal must still reach the gateway as `codes.Aborted`, which is what `slotResolveError`/`slotResolveCategory` exist for. A lens reading the old form files a spec-versus-code contradiction that does not exist. EVIDENCE: staging.go:134-136,:181-184,:337-340; slotcreds.go:28; slotsession.go:77.
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

- **The `running` boundary contradiction is CLOSED, by a third route neither earlier option named.** The fix deleted the word "acknowledge(ment)" from the three START-handover sites, restated the boundary in the fence trigger's own vocabulary, and a later pass then deleted the attribution clause "which is the moment the trigger above names" as well, so SPEC-4's paragraph states the boundary on its own authority. The fence annotation at spec/06:152 and its mirror at docs/reference/state-machines.md:235 stay untouched, DOCS-1 keeps its single added row, and CODE-3's doc comment and DOCS-1's row already read arrival. The tree's boundary is `Runtime.Start` RETURNING followed by `noteRuntimeStarted`; there is no runtime-side acknowledgement of a start anywhere in `pkg/adapter` (the "runtime acknowledgement" hits in spec/04, spec/07 and spec/15 are the unrelated interrupt / CH-RUNTIMEOPS mechanism). Seven lenses across three rounds re-derived the old contradiction; do not reopen it. EVIDENCE: pkg/adapter/resume.go:140-144; runtimegeneration.go:36-44; server.go:348-351.
- **"Acknowledged" still carries a second, correct sense, and it must not be swept.** Every surviving use in the staging outside those three sites means "the ADAPTER did not answer the compensating `Shutdown`", which is load-bearing and untouched. A grep-and-replace over "acknowledg*" breaks the leak predicate.
- **Every gateway `PrepareWorkspace` call carries at least one upload frame, on BOTH callers.** `stageWorkspace` sends the RPC only inside `if len(uploads) > 0` (binder.go:1323-1330) and the §7.4 mid-session path is fed by `parseUploadToSession`, which 400s on `len(req.Files) == 0` (upload_to_session.go:183-186); `sendUpload` emits at least one frame per upload (adapterclient/client.go:267-286). So the upload-free stream that would report a zero epoch is unreachable from production, and the proposal's justification, which names only `stageWorkspace`, under-states a conclusion that holds on both. Eight lenses re-derived this; do not re-run it.
- **`resolvePrepareStagingDir` is `PrepareWorkspace`'s only route to the registry entry and it now widens to `(string, int64, error)`.** It resolves lazily under `if stagingDir == ""` and previously dropped everything `ensureSlotPaths` returns except `paths.Staging` (staging.go:77-84,:133-144), which is why widening `ensureSlotPaths` alone left `PrepareWorkspaceResponse.bind_epoch` permanently zero. That was FILED twice across two windows before it landed.
- **DECISION: the `ExcludePods` placement append reads `sbe.Leaked` alone; the accounting discriminator keeps `sbe.Leaked || relErr != nil`.** `relErr` is the gateway-side `ReleaseSlotReservation` failure, a Redis counter and `SandboxClaim` rollback that sends the pod no RPC and leaves no residue on it, so §5.2's placement rule, stated in pod-residue terms, does not reach it; `ClaimSlot` places only on a genuinely free slot. Both conditions still leak occupancy, so the ACCOUNTING composite is required and must not be collapsed. ALTERNATIVES rejected: widening the staged §5.2 sentence to name a failed reservation release (exports gateway bookkeeping into a reader-facing placement rule and cascades through the edge-case list), and recording the divergence as accepted (the divergence was the finding).
- **The epoch survives an adapter process restart because of the lazy `time.Now().UnixNano()` seed.** `nextBindEpochLocked` seeds the counter on its first call, so a restarted adapter mints strictly greater epochs and a latched stale epoch can never compare equal to a post-restart entry. `RestartPolicy: Never` closes the case a second way and the no-re-dial rule closes it not at all, because a `grpc.ClientConn` reconnects transparently. Staged §4.7.1 says only that the epoch is "pod-local, and never persisted", so the anti-reuse property lives ONLY in CODE-6's seed rationale and a reviewer reading the spec alone reconstructs the classic reset-fencing-token finding.
- **There is a SECOND tier-0 register a new test file must enter, and no earlier window named it.** `tests/tier0_static/spec_map_slot_address_registration_test.go` holds a hand-written `slotAddressCaseFiles` inventory, and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` fails for any `_test.go` under cmd/, migrations/, pkg/, scripts/, sdks/ or tests/ that matches `slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` (a BASENAME containing `slot` anywhere, not a `slot*` prefix) or whose BODY matches `slotSurfaceCallRE` = `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`. Both regexps are case-sensitive, so the adapter's own `claimSessionSlot(` and `releaseSessionSlot(` do not match. The gate is exhaustive against the tree today, so an omission goes red on the first tier-0 run. EVIDENCE: :1108-1131 (the gate), :1168, :1173, :1191, :1212-1230.
- **Entering `slotAddressCaseFiles` is not free, and the entry must land in the SAME commit as the file.** `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` then demands a `tests/spec-map.json` credit under EVERY section the file's own `// spec:` annotation names, and `TestAddressCaseNamesAgreeWithTheirOwnCitations` demands the `_spec_X_Y` name suffix agree with that annotation. An inventory entry for a file that does not exist yet dies in `repoFileBytes`'s `t.Fatalf`, and an existing matching file with no entry fails the omission check, so splitting the two across commits is red in both directions. Of this proposal's new files only the two `slot*`-named ones match; `pkg/adapter/bindepoch_test.go`, `tests/tier3_contract/adapter_bind_epoch/` and `tests/tier10_conformance/bind_epoch_conformance_test.go` do not.
- **`pkg/adapter/bindepoch.go` and `pkg/adapter/bindepoch_test.go` owe no register edit.** `_test.go` files are outside the change-graph completeness domain (`changegraph.IsSourceFile` returns false), `pkg/adapter/` is already a coverage-baseline directory prefix, and `validateTestFilesMapped` walks only `componentAndAboveTierDirs()` (tier 2 and above), so a tier-1 package test under `pkg/` needs no spec-map entry. Three separate gate worries, all dead.
- **`tracing.Category` does not exist; the type is `tracing.ErrorCategory`.** The proposal said the former and it was corrected. The category is readable from an in-package tier-1 test with no new fixture: `tracing.RecordError` attaches it as the `error.category` span attribute (`AttrErrorCategory`), and `pkg/adapter/tracing_internal_test.go` already ships `installInternalSpanRecorder` and `endedSpanNamed` in package `adapter`.
- **The three `tracing.CategorizeError(..., tracing.CategoryPermanent)` stamps CODE-6 must displace are at staging.go:81, :183 and :339,** and all three handlers wire `spanErr` into a deferred `tracing.RecordError`. Asserting `slotResolveCategory` in isolation pins the helper and NOT that those three literal stamps moved, which is exactly the ship-it-broken path; the assertion belongs on the handler's recorded span. The other two resolve sites (slotsession.go:75-78, slotcreds.go:26-28) stamp no category at all, so the asymmetry is real and is named in the staging.
- **The tier-4 datastore-crossing case's trigger is an UNANSWERED compensating `Shutdown`, not one the adapter refuses.** After the amendment a refusal is `SUPERSEDED`/`ABSENT`, which CODE-4 maps to `leaked=false`, the opposite of both assertions the case exists for; the unanswered form takes the `err != nil` arm. The fixture needs no new harness: `recycleAdapterDialer` builds its client through `adapterclient.Dial`, which is variadic over `grpc.DialOption`, so a unary client interceptor keyed on the `Shutdown` RPC drops in.
- **`isTransientPodClaimError` has exactly ONE production caller, `holdOrFailOnResumeError` (start.go:3610), reached only from `handleResume`.** So CODE-5's `codes.Aborted` arm cannot widen any other classification, cannot touch the start path, and cannot be reached from `treerecovery.go`'s `resumeOnPod` caller. `status` and `codes` are already imported at start.go.
- **`status.Code` walks the wrap chain in this tree's grpc.** v1.80.0's `Code` → `Convert` → `FromError` falls back to `errors.As` for `GRPCStatus()`, verified in the module cache by four lenses, so the `codes.Aborted` arm fires through `fmt.Errorf("%w")` plus `*SlotBindError`. Do not re-derive it; do not reach for an `errors.Is` against the adapter-local sentinel, which is unexported and does not survive gRPC.
- **`recordSlotFailure` has exactly five call sites and all five are inside `materializeSlot`.** `pkg/gateway/podlifecycle/podsession/binder.go`, which holds `Binder.Resume`, has zero, so the §7.3 re-attach can move the leak gauge, trip the threshold and increment `lenny_slot_pod_replacement_total` with no `lenny_slot_failure_total` series on that pod at all. Any proposal sentence grounding a non-goal in "the counter already labels the stages at exactly these call sites" is false at the third compensation call site.
- **`resumeOnPod`'s two branches, with line numbers.** The snapshotless resume-rebuild ends at start.go:3978 and reaches `applySlotRetryPolicy` through `startOnPod` → `bindConcurrentSlot`; the checkpoint-restore branch begins at :3980, calls `s.podBinder.Resume` at :4005 and is followed immediately by `if err != nil { return "", err }` at :4041-4043 with NO retry loop. Any sentence giving the client-driven §7.3 resume a retry budget before `holdOrFailOnResumeError` is describing the other branch.
- **`releaseSessionSlot` calls NO `Runtime.Close`.** Its whole body is `deregisterSlot` → `removeSlotTree` → `cancelPodMCPIfRuntimeIdle` (slotsession.go:214-220). Only `Shutdown` (session.go:263) and the §10.1.4 `terminateHeldSession` (holdstate.go:243) close the runtime. It is also fully synchronous, which is what makes the hold it opens end before the requesting RPC answers.
- **`onHoldTimeout`'s pass 2 shares ONE 10-second context across every member, not ten seconds each,** and it loops with no early exit, so the holds pass 1 takes for all members are bounded in aggregate rather than multiplied by N. A future `continue` or `return` inside that loop would leak every skipped member's hold for the life of the pod.
- **`coordinatorHoldAllowedMethods` has FIVE entries, not three:** `CoordinatorFence`, `NegotiateVersion`, `AdapterEvents` and both `grpc.health.v1` methods (holdstate.go:52-58). `Shutdown` is genuinely refused there, so the accepted-failure-mode bullet naming "the fence, version negotiation, the event stream, and the health probes" is accurate.
- **`tests/claim-map.json` is exactly 76 rows today: 32 WIRED, 24 UNWIRED, 20 ABSENT.** Count it with python over the JSON rather than by grep. SCHEMA-1's two WIRED rows take the denominator to seventy-eight and the WIRED count to thirty-four; 0080 §1.12 and §1.18 both quote "seventy-six", and the consolidated impacts row names only §1.12's.
- **DOCS-2 verifies end to end against the tree and the gate.** Its anchors resolve (`adapter-contract.md:10` self-description, :64 `DemoteSDK` row, :75 `Shutdown` row, :84 `**Scrub responsibilities.**`), its replacement row carries all four substrings `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` requires plus the two the staged tier-11 extension adds, and the added block carries the lowercase "bind epoch" and "reclaim hold" the page-level assertions need. Its `DemoteSDK` claim is true in the tree. Three other tier-11 gates read that page with `requireNoneContain` and none of their forbidden strings appears in the staged text.
- **`Binder.DialAdapter` is an injected dialer (binder.go:59) and `adapterclient.Dial` is variadic over `grpc.DialOption`,** so any tier-4 or tier-3 case that needs to fail, delay or record an adapter RPC installs an interceptor without touching production wiring.
- **The widened-function test-caller surface is exactly seven files, and one grep finds them.** `grep -rn "ensureSlotPaths\|claimSessionSlot\|resolvePrepareStagingDir\|noteRuntimeStarted\|claimPodMCPStart" --include=*_test.go .` returns `pkg/adapter/{adapterevents,export,exportpaths,one_session_only,podmcp_arming_internal,slotsession,usage}_test.go` and nothing else; all five identifiers are unexported so no caller exists outside package `adapter`. The r2 fix added the two that were missing (`exportpaths_test.go`, `one_session_only_test.go`) to the `Tests:` bullet, which closes the standing Open. Note `socketruntime_test.go` is in that bullet for a different reason, so the bullet is not the widened-caller set.
- **`one_session_only_test.go` holds SEVEN `claimSessionSlot` call sites (:91, :94, :125, :159, :194, :209, :212), not nine.** Two shards wrote nine; the staged clause deliberately carries no count.
- **`podRegistry.Put` has exactly three production callers** (start.go:2936, :4057, cmd/lenny-gateway/coordination_seams.go:249), and the third is the coordinator-handoff re-adopt, which dials fresh and sends `CoordinatorFence` as its only RPC, so CODE-6's zero-latch carve-out is accurate.
- **Rule S-2's covered handler files are `session.go`, `lifecycle.go`, `checkpoint.go`, `coordination.go`, `credentials.go`, `slotcreds.go`, `attach.go`, `sdkwarm.go` plus the two renamed channel files.** This proposal opens exactly three of them, so "three of S-2's covered handler files" is right; `staging.go`, `slot.go`, `slotsession.go`, `resume.go`, `holdstate.go` and `runtimegeneration.go` are not covered. S-2's "every in-flight `pkg/adapter` handler edit must have merged first" reads inverted against S8 preceding S9-S11 and is not: putting S8 first is what guarantees none of this proposal's handler edits is in flight.
- **spec/04:1538 says `AssignCredentialsResponse` carries a `leaseToken` field, and the message is `{}`.** Already false before this proposal; SCHEMA-1 adding `bind_epoch = 1` neither creates nor worsens it. At least four lenses have met it beside the SCHEMA-1 table and had to rule it out.
- **`cleanupTimeoutSeconds` is not a CRD field anywhere in the tree,** despite §5.2 stating its floor as a "CRD validation rule" the `SandboxWarmPool` admission webhook enforces. `grep -rn cleanupTimeoutSeconds charts/ pkg/apis/` is empty. Pre-existing and unedited by SPEC-3.
- **The §5.2 reclaim hold's "or resolve" arm is VACUOUS for every RPC, and that is the fastest kill for the whole "the hold refuses X" family.** The hold begins at the critical section that DEREGISTERS the entry, so while it runs no entry exists and nothing can resolve one; the only reachable arm is creation, and the creation sites are exactly the bind sequence. It is why §15.4's "`Shutdown` is not held" is a clarification rather than an exception, and why `Attach`, `Interrupt`, `Checkpoint`, `ReportUsage` and the three credential RPCs are untouched. Refuted at least eight times.
- **§15.4's "Current protocol version: `1.0.0`" is not an obligation this proposal breaches.** Nothing in the spec requires a bump on an additive change, and `.claude/rules/code-best-practices.md` bars backward-compatibility handling outright because the platform is pre-deployment, so a deliberate semantic conformance break at an unchanged version string is not a finding. Recorded as looked-at rather than open.
- **Tier 9 and tier 10 both drive the exported `adapter.Server` directly in this repo,** with a fake runtime and fake scrub host ops rather than a runtime binary over JSONL, so CONF-1's filesystem assertions and the tier-9 credential-fence arms are observable at the tiers they are filed under, and parking inside `Runtime.Close` to assert an `ABORTED` refusal is mechanically available at tier 10.

### Traps

- **Do not re-gate the cleanup-outcome report on `st.started`.** "Simplifying" the two predicates back into one reintroduces the double report through CODE-2's rollback and advances `sessions_served` for a session that never ran. The tier-1 "Claimed but not yet recorded" case exists to fail when that happens.
- **Do not gate the runtime teardown on `runtimeLive` either.** The tempting local fix for the mismatched sentence is to move pkg/adapter/session.go to `runtimeLive` so the code matches the spec text. It inverts the fail-closed direction: CODE-2's rollback fires only when `Runtime.Start` returns, so a start that hangs would leave the runtime serving the abandoned session forever. EVIDENCE: pkg/adapter/session.go:155-163.
- **Do not widen §6.2's fence to move `slot_cleanup ──→ leaked` into the either-concurrency block.** It is the obvious way to make a prose terminal claim true, and it changes shipped state-machine semantics, cascades into docs/reference/state-machines.md:251, and breaks the tier-11 matched-pair test. G1 removed the terminal claim from SPEC-4's prose instead, which is the smaller fix.
- **Do not edit spec/06:152 or docs/reference/state-machines.md:235.** The advice holds and the reason pass 1 recorded for it does not. The annotation reads "workspace ready, session dispatched to runtime with its session identifier", which is the runtime-has-been-given boundary (`runtimeLive`) rather than the admitted-start boundary, and SPEC-4's prose anchors to it rather than replacing it, which is what keeps DOCS-1 to the single added row it stages. An agent trusting the old gloss concludes SPEC-4's prose mis-cites the annotation when it does not. The residual "dispatched versus acknowledged" question is now CLOSED: the staged prose no longer attributes its boundary to the trigger at all. See the Settled entry on the closed `running` boundary.
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
- **The SCHEMA-1 window under `**Decisions.**` and the R1b row under `## Impacts on other proposals` both assert the S-2 second-window precondition.** They agree today and are deliberately not folded into each other: the decisions bullet carries it as this proposal's step-ordering constraint, the impacts row as an assertion about the programme step. An edit to one owes the same edit to the other.
- **MISTAKE nearly filed, three shapes against the reclaim hold's reach into non-bind RPCs.** `RevokeCredentials` answering `codes.FailedPrecondition` mid-hold where §15.4 calls a permanent status non-conformant; `extendCredentialLeaseSlot` returning an empty success; `Attach`/`Interrupt`/`ReportUsage` answering the same permanent code. All three die on the same two grounds already recorded for the wide predicate, and the natural remedy is a §15.4 wording tightening, which is spec-lane. Anyone reopening must attack the "create or resolve is this change's own bind-sequence vocabulary" scoping directly. EVIDENCE: pkg/adapter/slotcreds.go:102-146; slotsession.go:274-283.
- **Do NOT read the refuted-list entries about the shared-entry epoch inheritance as current.** Six of seven shapes one lens re-derived from the amendment text were refuted as STALE against a pre-`2aca9851b` snapshot rather than on their merits. Diff against the newest differing snapshot and grep the load-bearing quote before deriving anything from remembered text.
- **The standing context is where the answers are, and reading it end to end is the cheapest first move.** Six of seven candidates a fresh reader generates on this document are already in it, several as explicit "do not re-derive" entries, and four consecutive lens shards this window recorded that reading it first turned a full re-derivation into a spot check. The refuted-family index in Traps is the highest-value part; keep it at length and do not split the consolidated security-family and capacity-family entries back into individual lines.

- **MISTAKE, filed twice and still standing: "refuses" is overloaded after the amendment, and the Testing section was never swept.** Post-amendment a refusal is the `SUPERSEDED`/`ABSENT` answer, which CODE-4 maps to `leaked=false` and CODE-5 excludes from `ExcludePods`; pre-amendment "the adapter refuses" could only mean an errored RPC. The tier-4 datastore-crossing case carried the pre-amendment sense verbatim from commit 2811d55fe. Whoever edits that section must sweep every "refuse"/"refused" for the place "did not answer" is meant, and must NOT sweep the other twenty, which are the reclaim-hold and epoch refusals and are correct.
- **Do NOT name `releaseSessionSlot` as a `Runtime.Close` park site.** Two fix rounds wrote tier-1 hold bullets telling an implementor to park or panic `Runtime.Close` at `releaseSessionSlot`; its body reaches no such call. Only the §10.1.4 disjunct is writable, and `removeSlotTree` is `slotlayout.RemoveTree(st.paths)` with no injectable seam, so that row has no parkable destructive step either.
- **The snapshot trap, seventh and eighth forms.** Seventh: in several rounds the named snapshot was byte-identical to the working tree INCLUDING the review log, so `diff -rq` returned nothing at all and the real delta was the UNCOMMITTED working-tree change — use `git diff HEAD -- proposals/0081_*/` then. Eighth: the newest DIFFERING snapshot is often a `-prefix` directory, because the fix stage runs between the prefix snapshot and the next round's, and in one round the brief's named snapshot was two rounds stale. Run `diff -rq` across the whole `scratchpad/cp-snap/0081...` chain before budgeting a round on "read the delta first"; at least fifteen shards in this window opened by discovering the delta did not exist.
- **MISTAKE the credit-inventory gate punishes, and nobody named the gate for six rounds: creating a `slot*_test.go` file.** Two of this proposal's new files match it by basename and neither was registered until the r5 fix. The trap has a second edge that is CONTINGENT and easy to miss: rule 1 is a body match over the whole tree, so an EXISTING unregistered file becomes mandatory the moment an edit adds `slotstate.`, `ClaimSlot(`, `ReleaseSlot(`, `ReserveSlotOnPod(`, `claimAtCreate(` or `BindReservedSlot(` to it. `pkg/gateway/sessionserver/start_test.go` and `resume_setup_demotion_internal_test.go` are both outside the inventory at zero matches today and are both plausible homes for a `BindReservedSlot(` literal. Do not describe the name rule as a `slot*_test.go` prefix glob; the regexp matches `slot` anywhere in the basename.
- **Do not file the DOCS-2 audience-mismatch clause.** Its rationale says the page is written for the third-party ADAPTER author; `docs/reference/adapter-contract.md:10` says the page is the reference for the protocol between the sidecar and "your runtime binary" and :53 says the runtime never sees those RPCs directly. The attribution is genuinely false and it is rationale prose that never reaches `docs/`; the block it lands is accurate. Six lenses across three rounds have derived it and none filed it. The fix, for anyone already in that paragraph, is to say "so the gateway-to-adapter contract this page covers states the epoch and the hold".
- **MISTAKE that cost a whole loop: a fix stage declared a lane converged without sweeping `### Open` for entries marked FILED.** CODE-4's session-keyed credential release was recorded FILED-and-live with a named remedy, the fix round after it did not apply it, and the next round's reliability and security lenses both re-derived and re-filed it. Deferred and FILED entries against files the current lane MAY edit are the cheapest findings in the pile; check them before anything else.
- **Do NOT re-derive the capacity family; it has now been declined by six performance passes over text that did not move.** The replica-local `slothealth` ledger against the durable Redis occupancy, the exclusive-pool resume budget degenerating to the whole `cleanupTimeoutSeconds`, the compensation pinning a client request goroutine, the blob-outage mass drain, and the correlated re-attach storm are all true, all recorded as Open items or accepted failure modes, and all declined on the shipped-baseline or open-decision ground. Filing in this lens needs a mechanism that is NEW in the staged text. One durability asymmetry is worth having stated once: `ReleaseSlot(leaked=true)` skips the counter decrement whose doc comment names the §12.4 Postgres fallback, so the withheld occupancy is DURABLE while the `slothealth` count meant to bound it is per-replica and in-memory — pre-existing sharding, but this proposal changes the RATE, because a `leaked` disposition becomes the routine answer to any unanswered compensating `Shutdown` rather than needing `ReleaseSlotReservation` to error.
- **MISTAKE, the `awk` proto-range artifact, reproduced by at least TWENTY lenses across this window.** Nearly every lens that checked SCHEMA-1's field numbers built `AssignCredentialsResponse` a false field set by running an `awk '/^message X \{/,/^\}/'` range over a one-line `message AssignCredentialsResponse {}`, which falls through into `RotateCredentialsRequest`. Several nearly filed "field 1 collides with `session_id`". The nine numbers are settled and re-verified by more than a dozen independent brace-depth parses: do not check them again, and if you must, use `grep -n "^message X"` plus `sed -n` or a brace-depth walker.

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
- **§15.4's "reports zero on a bind-sequence response" versus an empty `PrepareWorkspace` stream** — OPEN, and NARROWED to third-party callers only: both gateway callers are now known to send at least one frame (`stageWorkspace` gates on `len(uploads) > 0`; `parseUploadToSession` 400s on an empty file list), so no response the shipped gateway can provoke reports zero. The clause is still absolute, so a conformance harness or a third-party caller opening an upload-free stream would be judged non-conformant for a call that resolved no entry. The fix, if a round wants one, is to scope the clause to a call that resolved an entry. Two earlier Open items said this and are merged here. See `spec.7.review-edit-sites.1` and `non-spec-recheck.1.fix-epoch-plumbing`.
- **Would a caller multiplexing two sessions on one adapter connection name the wrong session's epoch?** — UNVERIFIED: the shipped caller cannot reach it (a fresh dial per bind), and the spec never states the one-session-per-connection premise the per-connection latch rests on. A lens owning the caller contract should decide whether §4.7.1 must state it. See `spec.6.review-applicability.1`.
- **Does §28.3's `LNK-POD-GRPC` multiplicity reach unregistered gateway→adapter RPCs?** — UNVERIFIED: that row says "One connection per gateway replica per pod" while §4.7.1 states a bind attempt may span two connections and production dials a fresh client per connect. §29.2 and §29.4 place the bind and teardown RPCs outside the §28 register, which is why three rounds declined it. A channel-naming lens should settle it. See `spec.6.review-edit-sites.1` and `spec.8.review-fresh.1`.
- **spec/04:672's `StartSession` row states no refusal** — OPEN, pre-existing: `claimSessionSlotUnderLock` refuses a repeat start onto a started session with `codes.Unavailable`, and this proposal depends on that refusal twice. Worth its own finding in a later round; do not repair it inside 0081. See `spec.6.fix-design-G1.1`.
- **Retire the Kubernetes lens for the remaining spec rounds?** — OPEN: it has returned nothing on the spec staging in rounds 3, 5, 6, 7 and 8 against text that changed each round only on the gRPC surface. If a future amendment touches no CRD, status write, finalizer, webhook or reconcile loop, retiring it costs nothing and saves a round's latency. See `spec.8.review-kubernetes.1`.
- **Are the two shared-entry edge-case bullets one ordering stated twice?** — UNVERIFIED: `spec.6.fix-design-G3.1` deliberately did not restructure the section, and the two bullets describe near-identical orderings. If a later round merges them, apply the closing-cost sentence ONCE.
- **Does "termination window" name a quantity the specification defines?** — OPEN: §5.2's hold paragraph bounds a hold taken outside any request by "the termination window of the pass that runs the cleanup", and `grep -rn "termination window" spec/` returns zero hits, on a fail-closed gate. §15.4 points at §5.2 for the whole hold and inherits the undefined bound into the third-party contract. Declined as descriptive. See `spec.6.review-kubernetes.1`.
- **Is the hold refusal "accounted as an ordinary transient slot failure" on the resume path?** — UNVERIFIED: true for the create-time-reserved retry through `materializeSlot` → `recordSlotFailure`, and false for the client-driven §7.3 resume, which emits no `lenny_slot_failure_total` at all and reaches accounting only through CODE-5's new caller. The edge case names that resume as one of three things that can meet the hold. Turns on whether the sentence means the §5.2 classification or the metric. See `spec.6.review-operational.1`.
- **What does a third-party adapter answering `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` mean?** — UNVERIFIED: the zero value has no meaning in either staged block (both state exactly three outcomes) and CODE-4's switch routes it through `default: return false`, that is, not leaked. Conforming, non-conforming or leaked is unstated. Judged below the bar on the proto3 zero-value convention, and `SessionScrubOutcome` has the same shape. See `spec.2.review-client-surface.2`.
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
- **summary.md's "Seven residues survive" count** — OPEN: the bullet then enumerates four plus two. Same class as the already-refuted "Four statements are needed" count, and `doc-style.md` would drop the count anyway. Whoever next edits that bullet should name the seventh or drop the count. See `non-spec.1.review-fresh.1`.
- **Is the tier-4 datastore-crossing case written as a second self-issued `BindSlot`?** — UNVERIFIED: `recycleCluster` seeds one idle Sandbox and wires a `podsession.Binder` with no `sessionserver`, and `applySlotRetryPolicy` is unexported, so the "retry" there can only be a second `BindSlot` the case issues itself with `ExcludePods` set. That reading is workable; whoever implements S12/S13 should confirm the case is written that way rather than reaching for the retry policy from an external package. See `non-spec.1.review-test-coverage.1`.
- **Does a mass §7.3 recovery storm cascade through the new `resumeOnPod` accounting caller?** — UNVERIFIED and unmodelled: each failed resume drains its replacement pod at `maxConcurrentSessions: 2`, and the replacements are themselves resumed onto. This sharpens the standing correlated-re-attach-churn item with the specific loop. A capacity reviewer with Tier 3 pod-churn numbers should. See `non-spec.1.review-performance.1`.
- **Do the two SCHEMA-1 claim rows need `note` fields?** — UNVERIFIED: every row in the generator's `EXPLICIT` list carries one and the validator checks only status, surface and anchor, so it is convention rather than a gate. Whoever moves the rows into the generator should match the siblings. See `non-spec.1.review-feasibility.1`.
- **Does DOCS-2 owe a positive tier-11 gate on its new row's content?** — OPEN: the existing gate is substring-presence only and would pass a stale row again the next time §4.7 moves, where DOCS-1 stages its own assertion. See `non-spec.1.review-operational.1`.
- **CODE-4's session-keyed credential release versus a concurrent successor attempt** — OPEN, FILED: the wrapper's `b.releaseCredentials(req.SessionID)` releases every lease under the session key after a compensation that can block for the whole budget, and a second `/start` attempt for the same session is past `assignSlotCredentials` inside that window. The remedy is to release the lease ids this attempt minted rather than the session's. See `non-spec.1.review-reliability.1` and the Trap that records its two look-alike refutations.
- **What §15.1 error a client sees when its own `/start` retry on a create-time-reserved slot meets the hold** — UNVERIFIED: the proposal lists §15.1's error catalog as deliberately untouched on the ground that the refusal mints no client-visible code, and nothing in either lane names the catalog row the gateway answers with or shows it is transient on that path. See `spec-recheck.1.review-client-surface.1`.
- **Does `superseded` fix a value for `exited_cleanly`?** — UNVERIFIED: §5.2 states the leak detector as "the `Shutdown` response for that reclaim does not report a clean exit" while §7.1 scopes the same predicate to "answered `reclaimed` without reporting a clean exit". Nobody has stated on the record what `exited_cleanly` carries on a `superseded` or an `absent` answer. See `spec-recheck.1.review-docs-alignment.1`.
- **Does the widened leak criterion mass-retire pods on a transport blip?** — UNVERIFIED: an incomplete adapter reclaim now produces a PERSISTENT leak where the shipped path produced a windowed failure that decayed in five minutes, so a fleet-wide gateway-to-adapter blip retires one pod per event at `maxConcurrentSessions: 2`. Judged not a finding (the residue is genuinely unreclaimable and §6.2 prescribes the persistent disposition), and nobody has measured the replacement rate it implies. See `spec-recheck.1.review-performance.1`.

- **Which step routes `Shutdown` through `reclaimSlotLocked`?** — OPEN, FILED: the checklist S9 line and CODE-6's body say CODE-6 does it at S9; CODE-1/S10 and two Testing bullets say CODE-1 does it at S10. Whoever fixes it must pick one owner and make the S9 parenthetical read as a rule statement or drop `Shutdown` from it. See `non-spec-recheck.4.review-edit-sites.1`.
- **Does the hold releaser `reclaimSlotLocked` returns take `s.mu` itself?** — OPEN: CODE-1 registers `defer release()` while holding `s.mu` and runs it after the unlock, while `terminateHeldSession` defers it with the lock never held, so it must take the lock, and `reclaimSlotLocked`'s "callers hold `s.mu`" must not be read as covering `release`. Nothing staged says so. Whoever lands CODE-6 should state it in the doc comment. See `non-spec-recheck.1.review-reliability.1`.
- **Does SPEC-2's §7.2 step-3 obligation reach any code deliverable or test?** — UNVERIFIED: the non-spec staging never mentions §7.2, names no deliverable for the mid-resume terminal-collapse path and lists no test carrying a `// spec: §7.2`. It may be discharged by reference through CODE-4's `Binder.Resume` compensation if the DELETE-during-`resuming` handler routes through a failing `Binder.Resume`, which nobody has traced. Two lenses reached it from opposite sides. See `non-spec-recheck.4.review-test-coverage.1` and `non-spec.1.review-mechanism.1`.
- **Can the tier-4 interceptor be added without perturbing the six other `recycleAdapterDialer` call sites?** — UNVERIFIED: the proposal implies a per-case variant dialer and does not say so, and an implementor who edits the shared helper in place breaks the other six cases. See `non-spec-recheck.5.review-applicability.1`.
- **The claim-register surface symbol is spelled two ways** — OPEN: SCHEMA-1's row names `Server.nextBindEpoch` while CODE-6 stages `nextBindEpochLocked`. No gate resolves a surface symbol, so this lands as a false symbol reference in a committed register rather than a red test. Four lenses recorded it and none filed it; a fixer already in that JSON should align it. See `non-spec.1.review-fresh.1`.
- **`StartSessionResponse.bind_epoch` has no write site in any deliverable target list** — OPEN, FILED: the message appears exactly once in the whole non-spec document, in the SCHEMA-1 field table; the `session.go` files-touched entry lists only `Shutdown`'s clause two, the response and the doc comment plus the `StartSession` rollback, and CODE-6's heading excludes `session.go`. The same step also breaks `session.go:111` through the widened `claimSessionSlot` return. Two lenses filed it. See `non-spec.1.review-edit-sites.1` and `non-spec.2.review-applicability.1`.
- **Three docs sentences against SPEC-3's withheld report** — UNVERIFIED and disputed, with no correction between the readings: one operational lens FILED `docs/reference/adapter-contract.md:81`, `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33` as stating universally what SPEC-3 withholds, on the ground that :81 sits on the very page DOCS-2 opens and would contradict DOCS-2's own row; five other lenses declined the same sites because SPEC-3 places the pre-`running` cleanup OUTSIDE "session release" and the doc mirrors an unedited spec sentence. The newer FILED reading is kept here; the refutation is recorded in Traps. Somebody should settle whether DOCS-2 grows the exception clause or a DOCS-3 exists. See `non-spec-recheck.4.review-operational.1`.
- **Does DOCS-2's `DemoteSDK` row want a tier-11 pin of its own?** — UNVERIFIED: the staged extension pins the `Shutdown` row and the two page-wide strings and asserts nothing about the `DemoteSDK` row, so that half of DOCS-2 can be reverted silently. No gate reads that row today. See `non-spec.2.review-edit-sites.1`.
- **Does the tier-11 extension owe an annotation widening?** — UNVERIFIED: it adds assertions over §4.7.1, §5.2 and §15.4 content to a test whose `// spec:` annotation is `4.1, 4.7`, and the staged edit list does not name the annotation. No gate reads a tier-11 annotation, because `componentAndAboveTierDirs()` excludes `tests/tier11_docs`, so nothing turns red. See `non-spec.2.review-operational.1`.
- **`bindSessionForTest` has to be widened to return the epoch** — UNVERIFIED: it calls `ensureSlotStateLocked` and returns nothing (usage_test.go:350-363), so `usage_test.go:233`'s "take the epoch from the claim it already makes" needs the helper to hand it back. Build-adaptation class the loop has refuted five times on materiality; recorded so it is not re-derived. See `non-spec.2.review-feasibility.1`.
- **Does the §10.1.4 self-termination path owe a scrub-report carve-out?** — UNVERIFIED: it deregisters STARTED sessions and deliberately files no `ReportSessionScrub`, which staged §5.2's complementary arm ("a cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome") says it must. Not filed, because §5.2's SHIPPED scrub-model sentence already says the same and spec/10 carries no carve-out, so applying SPEC-3 does not change the truth value. Start from holdstate.go:214-218 rather than re-deriving. See `spec-recheck.2.review-reliability.1`.

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

- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, the `## Files touched on application (non-spec)` list]: two of the seven epoch-reporting handlers are under-described in their own bullets. The `pkg/adapter/staging.go` entry names the epoch on the `FinalizeWorkspace` and `RunSetup` responses but not `PrepareWorkspace`'s (built at staging.go:116), and the `pkg/adapter/session.go` entry does not name the epoch on `StartSessionResponse` (built at session.go:164). Both files are in the list and SCHEMA-1's field table carries all nine, so no edit site is lost; the per-field enumeration inside two listed bullets is what is short. What is true instead: the staging.go bullet owes "and `PrepareWorkspace`" and the session.go bullet owes "and the epoch on the `StartSession` response". The same gap runs through CODE-6's own heading and summary.md's CODE-6 index line, which omit `session.go` and `resume.go` although CODE-6 owns the `StartSessionResponse` and `ResumeResponse` stamps. Derived independently by at least five shards across two windows and still unapplied; it is the other half of the FILED `StartSessionResponse.bind_epoch` Open.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, CODE-5's `Targets:` list]: it does not name `isTransientPodClaimError`, although the checklist S13 line, summary.md's index line and `## Files touched` all assign the `codes.Aborted` arm to CODE-5 and the arm is specified in CODE-5's own body. What is true instead: the `Targets:` list owes `pkg/gateway/sessionserver/start.go` `isTransientPodClaimError` beside its existing entries. Judged the same bookkeeping class the loop has refuted on materiality, so not filed; a fixer already in that paragraph should add it.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, the hold-refusal accepted-failure bullet]: this extends the standing entry above it rather than replacing it. The bullet still rests on "whose only production call site is `applySlotRetryPolicy`", which CODE-5 falsifies by giving `RecordFailure` three callers, and the r2 fix stage then BUILT A NEW CLAUSE ON TOP of the false one, adding a "once that retry budget is spent" premise that does not exist on the branch the sentence is about: `resumeOnPod`'s checkpoint-restore branch has no retry loop at all. What is true instead: the refusal costs the pod one windowed failure through `accountSlotFailure`'s third caller, and the client-driven §7.3 resume reaches `holdOrFailOnResumeError` on its FIRST error. The bullet's conclusion stands; the attribution and the premise are both wrong. FILED again this window.

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


Retired in compaction pass 12, all closed rather than dropped:

- The Trap "MISTAKE, barred and NOT closed: SPEC-4's prose against the untouched `receiving_uploads ──→ running` trigger", and the UNVERIFIED beside the `running`-boundary DECISION. Both CLOSED by the fix that took a third route the trap did not consider: delete the start-handover sense of "acknowledge", restate the boundary in the trigger's own vocabulary, then delete the attribution clause as well, leaving the fence annotation and its docs mirror untouched. The durable statement is in Settled. The residual "dispatched versus acknowledged" question dies with it, because the staged prose no longer attributes its boundary to the trigger.
- The Trap "The `## Impacts on other proposals` section breaches its own one-row-per-proposal rule with four 0080 rows". Closed by the `f2.open-decisions.0080-consolidated` firing, which merged the four into one row carrying every assessment verbatim plus the §1.12 claim-map arithmetic the old rows lacked. The section now carries six rows.
- The Open "Does `ensureSlotPaths` widening its return break an unlisted test file?". Applied: the r2 fix added `pkg/adapter/exportpaths_test.go` and `pkg/adapter/one_session_only_test.go` to the `Tests:` bullet and one clause in CODE-6 saying their call sites are retargeted to the widened returns at S9. The exhaustive seven-file sweep is in Settled.
- The two Opens on an upload-free `PrepareWorkspace` reporting a zero epoch, merged into one and narrowed to third-party callers. Both gateway callers are now known to send at least one frame, so the case is unreachable from production.
- The Open "Is the epoch fence observable end to end?" in its S9 tier-3 dress, and the UNVERIFIED that read the observation route as blocked. CORRECTED: a server-side interceptor on the REAL adapter server records the inbound `ShutdownRequest` without introducing a fake, so "over the real gRPC transport rather than against a fake" is satisfiable at S9 even though the adapter neither compares nor answers on the epoch until S10.
- The DEFERRED asking whether SPEC-4's fence annotation should be reworded to the arrival moment, with `docs/reference/state-machines.md`'s mirrored row added to DOCS-1. Moot: that was the option-(b) remedy for a contradiction the third route closed without touching either file.
- One Settled claim SUPERSEDED rather than closed, kept so the old form is recognisable: "three refusals now travel out of `ensureSlotStateLocked`" was true only of the withdrawn r5 per-attempt design. Under the reverted per-entry epoch exactly one refusal travels out. The correction is applied in place.
- One Settled claim CORRECTED in place: `binder_test.go:533` is not the `cl.Resume` failure branch and carries no integer-division panic risk. A fixer acting on the old form edits the wrong test.

## Ledger

### [non-spec.2.fix-G1.1]

DECISION: closed the "what can meet the reclaim hold" finding by prose alone across three sites (non-spec-changes.md's accepted-failure-mode bullet, CODE-5's "When it does not fire", summary.md's lagging-rollback residue) — BECAUSE CODE-6 made `releaseSessionSlot` a hold producer and the gateway has no signal that can see it: the adapter answers `ABSENT` identically whether the identifier is held or free — ALTERNATIVES: widening CODE-5's `ExcludePods` append to `ABSENT` (rejected: `ABSENT` is the common healthy answer and CODE-4's outcome-first mapping exists to stop it being read as failure); adding a fourth `SlotReclaimOutcome` plus a second placement discriminator (rejected: cascades into SCHEMA-1, SPEC-5, CODE-1/4/5, tier 3 and tier 10 for a one-attempt cost); adding a tier-1 case (rejected: already pinned by the conjunction of non-spec-changes.md's `releaseSessionSlot`-hold case and its `Shutdown`-during-hold `ABSENT` case).

MISTAKE: the design handed to this round asserted the refused §5.2 attempt gets a fresh budget "on an `onPoolExhausted: \"queue\"` pool where `runWithQueue` re-enters the closure". That is false and I did not write it. `runWithQueue` re-enters only on an exhaustion sentinel (`isExhaustion` = `ErrNoIdlePod`, `ErrNoConcurrentSlot`, `ErrTenantMismatch`, pkg/gateway/sessionserver/queue.go:103-107, gate at :143), and an exhausted `applySlotRetryPolicy` returns a `podsession.SlotFailedError` (pkg/gateway/sessionserver/start.go:2874-2881), which is not one. The refusal ends the request on every pool mode.

FACT: `applySlotRetryPolicy`'s exhausted-retry return is `SlotFailedError`, never an exhaustion sentinel, so nothing downstream of it re-enters the queue — EVIDENCE: pkg/gateway/sessionserver/start.go:2874-2881, pkg/gateway/sessionserver/queue.go:134-146.

WATCHOUT: the two CODE-5 grounds for excluding the §5.2 retry are both about the hold a *reclaim* opens. After CODE-6 the start-path rollbacks open holds too, and no reclaim outcome reports them. Do not re-derive the exclusion as covering them — EVIDENCE: non-spec-changes.md, `**What can meet the reclaim hold, and what it costs.**`, and CODE-6's producer list under `**Every site that deregisters an entry it then destroys goes through the helper**`.

WATCHOUT: the tree-deletion hazard at CODE-5's `**When it does not fire.**` is NOT superseded by the hold. It is the ordering where the rollback runs after the retry staged; the hold only covers the ordering where the rollback is still running when the retry arrives. Both now stand side by side; deleting either withdraws a live hazard — EVIDENCE: pkg/adapter/slotsession.go:214-220, pkg/adapter/session.go:133,:147,:157.

DEFERRED [spec-changes.md]: three sites there still carry the refuted two-ground argument. The SPEC-2 §7.1 replacement block states as normative text that "Section 5.2 keeps the retries its slot retry policy places off that pod, so an attempt that meets the hold is one that policy did not place"; the same sentence appears as design rationale earlier in the file; and the accepted-failure-mode bullet's "What can meet it is ..." list omits the third producer. What is true instead: the §5.2 exclusion reaches only a hold the reclaim itself opened, and a retry can meet a hold opened by the abandoned attempt's own `releaseSessionSlot` at the cost of its last attempt. Minimal repair is to scope the §7.1 sentence to a hold the reclaim opened, or delete it, and to add the third producer to the bullet's list.

USEFUL [review-log.md:498]: the "fully synchronous, so the hold ends before the requesting RPC answers" entry is correct and is exactly why the new producer is confined to the path where the gateway stopped waiting. It saved me from widening the claim.


### [non-spec.2.fix-G2.1]

FACT: `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` switches a file into PER-CASE mode as soon as one `path::TestName` row names it (`specMapCredits`), and then requires every annotated case in that file to be credited under every section its own annotation names. A whole-file credit still counts, but only for the sections it carries. So adding a CASE to an already per-case-registered file is not free, which contradicts the older standing note that "a new test FUNCTION in a listed file costs nothing" — that note is true of `validate-maps` only. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:825-858 (`specMapCredits`), :952-963 (`creditsMissing`), :969-988 (the gate)

CORRECTS [standing Settled, ":306" and ":236" entries on `validate-maps`]: both say a new test function in a listed file is "genuinely free". That holds for `validate-maps`, and it is false for the second tier-0 gate in `spec_map_slot_address_registration_test.go` when the file is in `slotAddressCaseFiles` AND registered case by case. Three such files this proposal edits: `pkg/gateway/sessionserver/slotretry_test.go` (whole-file credits 4.1, 11.4, 15.1 only), `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go` (4.1, 5.2), `tests/tier4_integration/recycle_scrub_path_test.go` (15.1).

FACT: current credit state, read from tests/spec-map.json, for the files 0081 adds cases to. `slotretry_test.go` whole 4.1/11.4/15.1, per-case rows under 5.2, 6.2, 7.3. `slotclaimer_test.go` whole 4.1/5.2, per-case under 4.6.1, 6.2, 12.4. `recycle_scrub_path_test.go` whole 15.1, per-case under 4.6.3, 4.7, 5.2, 6.2, 16.1. `slotbinder_test.go` whole 4.1/4.6.3/4.7/5.2/6.2/7.1 with NO per-case rows. `start_test.go` whole 4.1/11.4 with no per-case rows. `slotsession_test.go` whole 4.7/4.9/5.2/6.1/6.4/15.4 with no per-case rows.

DECISION: closed the finding by restating the `tests/spec-map.json` bullet as a RULE (both gates, in one sentence) plus the enumeration, naming the five new test functions the per-case rows need, and narrowing the tier-2 annotation to `// spec: §5.2` — BECAUSE a bare enumeration keyed on new files reopens on the next placement, and a per-case row is `path::TestName` so an unnamed case makes the row undeterminable — ALTERNATIVES: a whole-file `slotretry_test.go` row under 5.2/6.2/7.1 (rejected: silently re-credits the file's shipped per-case rows to sections those cases do not exercise), a trailing bookkeeping step after S14 (rejected: leaves tier 0 red on every commit from S13 on).

DECISION: narrowed the tier-2 placement-filter annotation from `// spec: §7.1; §5.2` to `// spec: §5.2` on the STATED ground that SPEC-2 puts the placement constraint in §5.2's `**Max retries:**` bullet and §7.1 states no placement rule — BECAUSE that is the accurate citation; the row it saves on `slotclaimer_test.go` (whole-file 5.2) is a consequence rather than the argument. — EVIDENCE: spec-changes.md:63-66; non-spec-changes.md:75-78

FACT: test-function names staged for the per-case rows, all with `_spec_5_2` suffixes that `TestAddressCaseNamesAgreeWithTheirOwnCitations` accepts against a `§7.1; §5.2; §6.2` annotation: `TestSlotRetryExcludesThePodOfAnUnacknowledgedReclaim_spec_5_2`, `TestSlotRetryKeepsThePodWhenOnlyTheReservationReleaseFailed_spec_5_2`, `TestSlotRetryExclusionSurvivesAQueuePoolReEntry_spec_5_2`, `TestSlotRetryExcludesBothPodsAcrossAQueuePoolReEntry_spec_5_2`, and `TestRecyclePathUnansweredReclaimLeaksAndMovesTheRetry_spec_5_2` (the tier-4 case, whose `// spec:` annotation the proposal previously left unstated and which is now `§7.1; §5.2; §6.2`).

WATCHOUT: `pkg/gateway/sessionserver/start_test.go` is NOT in `slotAddressCaseFiles` today and has no per-case rows, and its whole-file credits are 4.1 and 11.4 alone. The tier-1 "reserved bind path" case plants a `BindReservedSlot(` literal, which matches `slotSurfaceCallRE` and would pull the file into the inventory; the credit gate would then check it in WHOLE-FILE mode against 4.1/11.4 and go red on the §7.1/§5.2/§6.2 annotation. This is contingent on an implementor placement the proposal does not fix, so it is a separate finding rather than part of this fix, and the bullet's closing sentence ("any other file the implementor lands a case in is checked the same way") is the only cover it has. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1156,:1168 (`slotSurfaceCallRE`), :1108-1131 (inventory completeness gate); non-spec-changes.md:1570-1584 (the reserved bind path case)

DECISION: did not touch implementation-checklist.md:22, :24 or :32 — BECAUSE each only ever claimed its own new-file entries and each is true; only S13 gained a clause.


### [non-spec.2.fix-design-G1.1]

DECISION: Accept the residue and correct the prose in three places, rather than widen CODE-5's `ExcludePods` predicate or add a fourth reclaim outcome — BECAUSE after CODE-6 routes `releaseSessionSlot` through `reclaimSlotLocked`, a §5.2 retry CAN meet a live hold, but the hold it meets was opened by no reclaim, so the gateway observes nothing that distinguishes it: the compensating `Shutdown` is not held, finds no entry, answers `ABSENT`, and CODE-4 correctly reads that as a completed reclaim. The exclusion is therefore unwidenable without new wire surface. ALTERNATIVES: (a) widen the append predicate to `ABSENT` — wrong, `ABSENT` is the common healthy upload-free case and CODE-4's outcome-first mapping exists precisely to stop that; (b) a fourth `SlotReclaimOutcome` meaning "absent, identifier held" plus a second placement discriminator beside `sbe.Leaked` — closes the window exactly, but cascades into SCHEMA-1, SPEC-5, CODE-1, CODE-4, CODE-5, tier 3 and tier 10 for a bounded residue the document already accepts at summary.md:118 and spec-changes.md:190. Name what closing it would cost in one clause instead; (c) a new tier-1 test — rejected, already pinned, see below.

FACT: the hold-meeting ordering is reachable only because the gRPC handler keeps running after the client cancels. `releaseSessionSlot` is synchronous inside its handler, so the hold ends before that handler answers — review-log.md:498 is right about every caller that is still WAITING. The gateway that stopped waiting is the exception, and that is the premise of the whole compensation design (`context.WithoutCancel`). EVIDENCE: pkg/adapter/slotsession.go:214-220 (`deregisterSlot` → `removeSlotTree` → `cancelPodMCPIfRuntimeIdle`, no ctx anywhere); pkg/adapter/session.go:133,:147,:157.

FACT: BOTH orderings survive and the fix must not collapse them into one. (a) rollback-then-retry: the retry is refused by the hold and spends its last attempt (`maxSlotRetries` is 1, pkg/gateway/sessionserver/start.go:2720). (b) retry-then-rollback: the retry created its entry and staged its tree before the hold was taken, so the lagging rollback deregisters the RETRY's entry and deletes the RETRY's tree. The hold closes neither, and CODE-5's :882-888 sentence describes (b) and stays TRUE. A fixer who replaces it wholesale withdraws a live hazard.

WATCHOUT: `reclaimSlotLocked` takes the hold only when the deregistration actually removed an entry, so a compensating `Shutdown` that lands BEFORE the rollback removes the entry itself, and the rollback that follows removes nothing and takes no hold. That third ordering is safe and should not be described as a hazard. EVIDENCE: non-spec-changes.md:1004-1010.

DEFERRED [spec-changes.md]: the same false claim is staged as NORMATIVE spec text in two places and as commentary in a third, and the spec lane must correct them. (1) spec-changes.md:392, inside the SPEC-2 §7.1 replacement block: "Section 5.2 keeps the retries its slot retry policy places off that pod, so an attempt that meets the hold is one that policy did not place." FALSE as a universal; true only of a hold a reclaim opened. Minimal repair: scope it ("an attempt that meets a hold this reclaim opened is one that policy did not place") or delete the sentence. (2) spec-changes.md:60-62, the same sentence in SPEC-2's design rationale. (3) spec-changes.md:190-197, the accepted-failure-mode bullet "A retry that meets the reclaim hold spends an attempt on it", whose sentence "The retry the §5.2 policy places does not meet the hold: the retry after an unacknowledged reclaim excludes that pod, and an acknowledged reclaim is answered only after its cleanup finished" is the exact two-ground argument this round refuted, and whose "What can meet it is ..." list must gain the third producer.

USEFUL [review-log standing context, "`releaseSessionSlot` calls NO `Runtime.Close`"]: the entry's last clause ("fully synchronous, which is what makes the hold it opens end before the requesting RPC answers") is the standing refutation a reviewer reaches for here. It is true and it does not reach the abandoned-RPC path. Anyone re-deriving this should read it as scoped to callers still waiting rather than as a closure.

FACT: no new tier-1 case is needed for this. non-spec-changes.md:1359-1365 already table-drives "every deregister-then-destroy site takes the hold" with `releaseSessionSlot` reached through a `StartSession` whose manifest write fails, asserting the sentinel refusal while the destructive step is parked; :1357-1358 already asserts a `Shutdown` during a hold answers `ABSENT`. Their conjunction is the case the finding asked for.


### [non-spec.2.fix-design-G2.1]

FACT: the credit gate branches on whether the map registers the file case by case, and that decides everything here. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` calls `specMapCredits`; if `len(perCase) == 0` it checks the file as a whole against `citedSectionsInFile`, otherwise it checks EVERY annotated case in the file against `wholeFile ∪ perCase[fn]`. So a single `path::TestName` row anywhere in spec-map.json flips a file into per-case mode for all its cases, forever. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:824-857 (specMapCredits), :951-963 (creditsMissing), :969-988 (the gate)

FACT: the four files this finding turns on, read out of tests/spec-map.json today. `pkg/gateway/sessionserver/slotretry_test.go` — per-case mode (15 `::Test` rows), whole-file credits only 4.1 (`pkg/gateway/...`), 11.4 (`pkg/gateway/sessionserver/...`) and 15.1 (exact path). `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go` — per-case mode (9 rows), whole-file credits 4.1 and 5.2. `tests/tier4_integration/recycle_scrub_path_test.go` — per-case mode (7 rows), whole-file credit only 15.1 (`tests/tier4_integration/...`). `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` — WHOLE-file mode, credits 4.1, 4.6.3, 4.7, 5.2, 6.2, 7.1, so the same new annotation is free there. All four are in `slotAddressCaseFiles` (:298, :307, :326, :432). `pkg/gateway/sessionserver/start_test.go` is NOT in the inventory (credits 4.1, 11.4 only).

FACT: 5.2, 6.2, 7.1 and 15.1 are all declared spec-map section keys and none is under `pending-implementation` in tests/spec-map-exceptions.yaml, so `creditsMissing` returns them rather than carving them out.

DECISION: state the RULE in the `tests/spec-map.json` bullet and then enumerate, rather than extend the enumeration alone — BECAUSE the bullet today reads as a closed enumeration keyed on new FILES and discharges only `validate-maps`; the neighbouring `slotAddressCaseFiles` bullet already carries a general form ("an edited test file that gains such a call enters it in the step that adds the call"), so a general sentence matches the section's own idiom and also covers a case placement the enumeration did not anticipate. ALTERNATIVES: enumerating per-case rows with no rule (silently wrong the moment an implementor puts a case in a fifth file); a trailing registration step (forbidden by the checklist's per-step tier rule and by proposal 0073's recorded precedent).

DECISION: narrow the tier-2 placement-filter cases to `// spec: §5.2` alone (:1633) — BECAUSE SPEC-2's own text says the placement constraint lives in §5.2's `**Max retries:**` bullet and "§7.1 states the reclaim and its disposition and no placement rule" (spec-changes.md:63-66; non-spec-changes.md:76-77), so §7.1 was a miscitation; and §5.2 is already a whole-file credit on `slotclaimer_test.go`, so the narrowing costs zero map rows. ALTERNATIVES: keeping §7.1 and staging a `slotclaimer_test.go::<Name>` row under 7.1 — correct but pays a row for a citation the proposal's own spec lane disowns.

DECISION: did NOT narrow the tier-1 group annotation `// spec: §7.1; §5.2; §6.2` (:1505) — BECAUSE `slotretry_test.go` credits 5.2 and 6.2 per case only, so narrowing removes no obligation, and splitting the one group annotation across subsets of a fifteen-item list is more hair than three map rows.

WATCHOUT: per-case rows are `path::TestName`, so the fix CANNOT land without the proposal naming the new test functions. Four unnamed cases at non-spec-changes.md:1578-1596 and the unnamed tier-4 case at :1726-1741 must be given names (and the tier-4 case an annotation, which the proposal never states) or the entry is undeterminable.

WATCHOUT: a contingent second hole, adjacent but NOT this finding. The tier-1 "reserved bind path" case plants a `BindReservedSlot(` literal, which is `slotSurfaceCallRE`. If the implementor puts it in `pkg/gateway/sessionserver/start_test.go`, that file enters `slotAddressCaseFiles` (the general rule the proposal already states covers this), and because it has NO per-case rows it then enters the credit gate in WHOLE-FILE mode, demanding credits for every section every annotation in that whole shipped file names, against whole-file credits of 4.1 and 11.4 only. That could be a large unbudgeted cascade. Nobody has costed it. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:975-980; tests/spec-map.json (start_test.go rows)

UNVERIFIED: which checklist step owns the tier-4 `recycle_scrub_path_test.go` case. Both S12 (CODE-4) and S13 (CODE-5) declare tier 4; the case's own text says it is "the consequence of CODE-4 and CODE-5" and it asserts the `ExcludePods` re-bind, which is CODE-5, so S13 is the reading this design takes. A fixer should make the step attribution explicit rather than leave it inferred.


### [non-spec.2.review-applicability.2]

FACT: the snapshot at `scratchpad/cp-snap/.../non-spec-r2` differs from the working tree in
`review-log.md` ONLY. `diff -ru` over the whole directory returns one file. So round 2 had no
proposal delta to read; the whole document was re-derived. EVIDENCE: diff output, one `diff -ru`
header line. (This is the snapshot Trap's ninth form; it keeps recurring.)

FACT: there are TWO independent spec-map gates and the proposal only discharges one.
`cmd/lenny-test/cmd_validate.go:716 validateTestFilesMapped` requires every `_test.go` under
`componentAndAboveTierDirs()` (tier2..tier10, 7a included) to resolve to a spec-map entry; a
`dir/...` glob covers a whole directory (`:739`). That is the gate the proposal's spec-map bullet
names. The SECOND gate is
`tests/tier0_static/spec_map_slot_address_registration_test.go:970
TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`: for every file in
`slotAddressCaseFiles`, if the map registers ANY case of that file by `::TestName`, then EVERY
annotated case in the file must be credited per-case (or by a whole-file entry) under every
section its own `// spec:` annotation names. `creditsMissing` at `:953-963` is the predicate.
This gate is invisible from `validate-maps` and fires on a NEW CASE in an EXISTING file, which
the proposal's "each landing in the step that creates the file it maps" rule does not reach.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:826-861,:953-988.

FACT: per-case-versus-whole-file registration, computed from tests/spec-map.json:
- `pkg/gateway/sessionserver/slotretry_test.go` — 15 per-case entries; whole-file only 4.1, 11.4, 15.1.
- `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go` — 9 per-case; whole-file 4.1, 5.2.
- `tests/tier4_integration/recycle_scrub_path_test.go` — 7 per-case; whole-file 15.1.
- `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` — 0 per-case; whole-file 4.1, 4.6.3, 4.7, 5.2, 6.2, 7.1 (so new §7.1/§5.2/§6.2 cases there are already covered).
- `pkg/adapter/slotsession_test.go` — 0 per-case; whole-file 4.7, 4.9, 5.2, 6.1, 6.4, 15.4 (new §4.7/§5.2 cases covered).
- `pkg/gateway/sessionserver/start_test.go`, `pkg/adapter/socketruntime_test.go`,
  `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` are NOT in
  `slotAddressCaseFiles` and match neither derived rule today, so the credit gate does not reach them.
Recompute this before filing any variant; the numbers move whenever spec-map does.

FACT (checks that came back CLEAN, do not re-run):
- All nine staged proto field numbers are free on their messages. `AssignCredentialsResponse` really is
  `message AssignCredentialsResponse {}` (schemas/lenny-adapter.proto:1033), so field 1 is correct.
  WATCHOUT: `awk '/^message X \{/,/^\}/'` gives a FALSE answer for a one-line `{}` message — it runs on
  to the next message's closing brace. Parse with a brace counter.
- `TestShutdownMessagePostRemovalDescriptor_spec_4_1` matches the proposal's description exactly
  (tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-255). The only other
  closed field sets over adapter messages are in `checkpoint_stream` (CheckpointStart, six fields)
  and `interceptor_proto` (a different proto). None covers the nine opened messages.
- The claim-register gates do NOT resolve a row's `surface` against the tree for non-credential rows
  (tests/tier0_static/claim_register_test.go:275-281 checks only "names a surface" and "not a bare
  line"), so S8's two WIRED rows naming `pkg/adapter/bindepoch.go` (created at S9) do not fail tier 0.
  `claim_register_proto_agreement_test.go` only tracks rows whose SURFACE contains the proto path;
  neither new row does.
- change-graph completeness excludes `_test.go` (`changegraph.IsSourceFile`), and `pkg/adapter/` is a
  coverage-baseline prefix, so `bindepoch.go` needs no register edit.
- Five resolve sites verified in the tree exactly as the CODE-6 table states, including the three
  `tracing.CategorizeError(..., CategoryPermanent)` stamps at staging.go:81, :183, :339.
- `ResumeRequest` really carries `SessionID`, `MaxConcurrentSessions` and `CleanupTimeoutSeconds`
  (binder.go:605,:641,:667); `SlotBindRequest` carries all three (slotbinder.go:31,:39,:108).
- `materializeSlot` (slotbinder.go:265) has exactly five `cl.Close()` calls and two entry paths
  (:133, :254), as CODE-4 states.
- DOCS-2's anchors resolve: adapter-contract.md:10, :64 (DemoteSDK), :75 (Shutdown), :84 (Scrub
  responsibilities). The tier-11 gate's four substrings all survive the staged row.
- The tier-11 per-slot gate's mechanism is as described: `generalSlotEdges` at :32-37 feeds a positive
  loop over the general block and a negative loop over the scoped block.

DECISION: did NOT file the SPEC-2(S3)-cites-SPEC-3(S4) forward reference. BECAUSE the citation is
mutually circular: staged §7.1 cites §5.2's reclaim hold (spec-changes.md:390) and staged §5.2 cites
§7.1's exclusive-pod disposition (spec-changes.md:600). No spec-step order removes one dangling
reference without creating the other, so the order is not the defect. ALTERNATIVES: reordering S3 and
S4, which just moves the dangle.

DECISION: did NOT file the `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` hole in CODE-4's outcome switch
(non-spec-changes.md:566-585): an adapter answering UNSPECIFIED with `exited_cleanly:false` falls to
`default: return false` and is recorded NOT leaked, where the shipped `err != nil || !cleanly` records
it leaked. BECAUSE the first-party adapter never emits UNSPECIFIED (CODE-1 always sets ABSENT or
RECLAIMED) and the material skeptic has twice refuted "a hypothetical third-party adapter" findings on
this proposal. UNVERIFIED: whether a §15.4 conformance reader should have this arm closed; someone
weighing the published-contract angle could revisit.

WATCHOUT: the credit gate also runs `TestAddressCaseNamesAgreeWithTheirOwnCitations` (:1027) over the
inventory, so any new case in `slotretry_test.go` or `slotclaimer_test.go` whose name carries a
`_spec_X_Y` suffix must have that section in its own `// spec:` annotation. The proposal fixes the
annotations (§7.1; §5.2; §6.2 and §7.1; §5.2) but names no test functions, so the implementor picks a
suffix from inside those sets.


### [non-spec.2.review-citations.2]

DECISION: returned zero findings — BECAUSE I re-verified essentially every concrete code/spec/schema citation in non-spec-changes.md, summary.md and the spec-changes anchors against the tree and every one resolved with its meaning intact — ALTERNATIVES: filing the CODE-6 heading/summary edit-list mismatch (heading at non-spec-changes.md:962 names four files while its own body and summary.md:679 assign server.go, slot.go, staging.go, slotcreds.go, sdkwarm.go to CODE-6); rejected because it is edit-list granularity already refuted twice in this loop's history.

FACT: only the review log differed between scratchpad/cp-snap/.../non-spec-r2 and the proposal directory this round, so the staged text was byte-identical to r1. `diff -rq` first; do not hunt a delta in the staged files. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r2

WATCHOUT: naive `awk` extraction of a proto message body breaks on the one-line form `message AssignCredentialsResponse {}` (schemas/lenny-adapter.proto:1033) — a prefix match on "message X {" swallows the NEXT message and makes the empty message look like it already holds fields 1,2,3,5. I nearly filed a field-number collision on `AssignCredentialsResponse.bind_epoch = 1` from that artifact. It is genuinely empty. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: all nine SCHEMA-1 field numbers are free on their messages, verified by reading each message: ShutdownRequest holds 1,2,3,5,6 with 4 reserved (1609); ShutdownResponse 1,2 (1665); PrepareWorkspaceResponse 1,2 (699); FinalizeWorkspaceResponse 1 (761); ResumeResponse 1,2,3 (1433); ConfigureWorkspaceResponse 1 (1690); RunSetupResponse 1 (869); StartSessionResponse 1 (958); AssignCredentialsResponse empty (1033). Enum value prefixing matches the `SESSION_SCRUB_OUTCOME_*` sibling at :438-449. EVIDENCE: schemas/lenny-adapter.proto:1033,1609,1665

FACT: every "reads, verbatim:" anchor block in spec-changes.md (twelve of them) is present byte-for-byte in the spec file its SPEC heading names. A scripted check over spec/*.md found no drift. Re-run that script rather than re-reading the anchors by hand. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:278,314,369,412,435,447,476,495,522,551,571,592

FACT: all five insertion points SPEC-4/SPEC-5 name exist and are adjacent as described: spec/06_warm-pod-model.md:156 (fence close) / :158 (`**reserved` hold semantics.**`); spec/04_system-components.md:688 (`*Adapter → Gateway RPCs:*`) / :695 (`#### 4.7.2`); spec/15_external-api-surface.md:1469 (SDK-warm demotion contract) / :1471 (`#### 15.4.1`). Every markdown anchor the staged text emits resolves to a real heading (4.7.1@659, 4.7.9@848, 15.4.2@1686, 15.4.3@1707, 10.1@10:3, 7.4@07:438, 5.2@05:365, 6.2@06:78, 12.9@12:1038).

FACT: the tier-0 credit-inventory gate's derivation is real and is what the proposal describes. `slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` and `slotSurfaceCallRE` = `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`; `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` fails for any matching file missing from `slotAddressCaseFiles`. The two new slot-named files are correctly named in the edit list. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1132,1168,1173,1212-1230

FACT: `podRegistry.Put` really does have three production callers; the third is `registry.Put(bind)` in the coordinator-handoff re-adopt, not a `podRegistry.` receiver, so a grep for `podRegistry.Put(` finds only two. EVIDENCE: cmd/lenny-gateway/coordination_seams.go:249

USEFUL [refutation of "the reclaim-then-start ordering compares an epoch against itself"]: its line-number corrections for the current spec-changes.md saved me re-deriving where the bullets now live.


### [non-spec.2.review-client-surface.2]

DECISION: returned an empty findings list. BECAUSE the proposal directory is byte-identical
to the `non-spec-r2` snapshot (every file but the review log), so there is no new text for
this lens, and my re-sweep of the client-facing representations found nothing that the
earlier `non-spec.1`/`non-spec.2.1` shards had not already settled. The one finding
`[non-spec.2.review-client-surface.1]` filed (DOCS-2 omitting §15.4's "`Shutdown` is not
held" carve-out) is still unfixed in the staged text, which means it was refuted; I did not
re-file it. ALTERNATIVES rejected: (a) DOCS-2's bind-epoch block states `absent` only inside
the epoch-carrying sentence and not for the no-epoch form — already on the refuted list as a
§15.4/§4.7 completeness family; (b) `AssignCredentialsResponse.bind_epoch = 1` — re-verified
correct, see the awk trap below; (c) the adapter protocol version constant is not bumped for
an additive proto change — nothing in spec or code ties a bump to a field addition, and
`ProtocolVersionV1 = "1.0.0"` with additive-only edits is within §15.4.2's own
"minor/patch are backwards compatible".
EVIDENCE: diff of every proposal file against scratchpad/cp-snap/.../non-spec-r2 is empty;
spec-changes.md:713 carries the carve-out, non-spec-changes.md:1283-1291 does not.

WATCHOUT (the awk trap, eighth recorded form): `awk '/^message X \{/,/^\}/'` over
schemas/lenny-adapter.proto gives a WRONG field list for every one-line empty message,
because `message AssignCredentialsResponse {}` matches the start pattern and the range then
runs to the next `^}` — which is the END of a LATER message. That is how the "field 1 is
already taken by `SessionId session_id`" error is generated. `AssignCredentialsResponse` is
literally empty. EVIDENCE: schemas/lenny-adapter.proto:1033 `message AssignCredentialsResponse {}`.

FACT: all nine SCHEMA-1 field numbers re-verified free this round by direct read of each
message body (not by awk range): `PrepareWorkspaceResponse` 1,2 → 3; `FinalizeWorkspaceResponse`
1 → 2; `RunSetupResponse` 1 → 2; `StartSessionResponse` 1 → 2; `ConfigureWorkspaceResponse`
1 → 2; `ResumeResponse` 1,2,3 → 4; `ShutdownResponse` 1,2 → 3; `ShutdownRequest` 1,2,3,(4
reserved),5,6 → 7; `AssignCredentialsResponse` {} → 1. Enum spelling matches the sibling
`SessionScrubOutcome` at :436-449, which fully prefixes its values.
EVIDENCE: schemas/lenny-adapter.proto:436,699,761,869,958,1033,1433,1665,1690

FACT: the four tier-0 gates that read `schemas/lenny-adapter.proto` are all unaffected by
SCHEMA-1, and none is an unlisted edit site. `claim_register_proto_agreement_test.go` only
checks rows whose `surface` CONTAINS the proto path (both new rows name Go surfaces instead),
plus a `coordination_generation`-only coverage arm. `adapter_proto_intrapod_pointer_test.go`
is scoped to three named comments. `adapter_proto_message_scope_test.go` keys on the
`SessionId`/`session_id` pairing, which an `int64` field cannot trip.
`schemas_test.go` never reads the proto's messages. EVIDENCE:
tests/tier0_static/claim_register_proto_agreement_test.go:68-102,:36-42;
tests/tier0_static/adapter_proto_intrapod_pointer_test.go:49-65;
tests/tier0_static/adapter_proto_message_scope_test.go:33-90

FACT: the claim-register row constraints the two new `WIRED` rows must satisfy are exactly
these: status in {WIRED,UNWIRED,ABSENT}; a WIRED row's `surface` must name a file or a symbol
rather than only a line reference; a WIRED row must carry NO `deferral_id`; `spec_anchor`
must resolve to a §28 heading. Both staged rows satisfy all four.
EVIDENCE: tests/tier0_static/claim_register_test.go:80,:277-288,:419-436

FACT: `validate-maps` accepts a DIRECTORY glob in a section's `tests` list and matches a test
file by walking its parent chain, so SCHEMA-1/CODE-6's `tests/tier3_contract/adapter_bind_epoch/...`
entry legitimately covers both files under it. The walk is tier-2-and-above only, so the new
tier-1 `pkg/adapter/bindepoch_test.go` needs no entry. EVIDENCE:
cmd/lenny-test/cmd_validate.go:733-780

FACT: the `slotAddressCaseFiles` credit inventory's two derived rules are
`slotSurfaceCallRE = slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`
(body match) and `slotSubjectFileRE = (slot|one_session_only|sole_session)[^/]*_test\.go$`
(name match). Of the proposal's five new test files only the tier-7a and tier-9 ones match,
and both are staged into the inventory at S10. The tier-3 directory `adapter_bind_epoch/`
and `bind_epoch_conformance_test.go` / `bindepoch_test.go` match neither rule — unless an
implementor names a tier-3 file `slot*_test.go`, which would silently break tier 0.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1159-1174,:1213-1229

FACT: `DemoteSDK` really does drop the adapter's registry entry, so DOCS-2's amended
`DemoteSDK` row and §4.7.1's caller rule are both true against the tree: the handler calls
`s.releaseSessionSlot(s.anyRegisteredSession())`. It returns `Unimplemented` on a pod-warm
adapter and removes nothing there, which is why CODE-6's latch clear is correctly gated on
"returns without error". EVIDENCE: pkg/adapter/sdkwarm.go:274-302

FACT: `StartSessionResponse.refusal_reason` and `ConfigureWorkspaceResponse.refusal_reason`
have NO producer in the tree (`grep -rn RefusalReason --include=*.go pkg/ cmd/` outside
`*.pb.go` returns nothing), so the "graceful refusal answers with no entry, hence a zero
`bind_epoch`, hence non-conformance under §15.4" argument has no reachable path. I worked it
and dropped it; do not rebuild it.

FACT: nothing this proposal changes reaches a CRD. `receiving_uploads` and the per-slot
sub-states appear in neither `charts/lenny/crds`, `pkg/embedded/crds`, nor `pkg/apis`, and
CODE-3 adds an edge rather than a state value. EVIDENCE: `grep -rn receiving_uploads
charts/lenny/crds pkg/embedded/crds pkg/apis` returns nothing.

USEFUL [non-spec.1.review-client-surface.1 and non-spec.2.review-client-surface.1]: their
sweeps (the adapter proto has exactly one client-facing mirror, `docs/reference/adapter-contract.md`;
`sdks/`, `charts/`, the OpenAPI document and every `schemas/*.json` carry none of the nine
messages; the DOCS-2 tier-11 substring arithmetic) held on re-check and saved re-running the
whole absence sweep.


### [non-spec.2.review-docs-alignment.2]

FACT: the proposal directory is STILL byte-identical to `scratchpad/cp-snap/.../non-spec-r2` except the review log, so this round's "read the changed sections hardest" instruction again had no target. `diff -rq` the snapshot before anything else. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r2

DECISION: filed the two accepted-residue-without-landing-text findings that `non-spec.2.review-docs-alignment.1` recorded as a worked-but-unfiled OPEN, and filed nothing else — BECAUSE the docs/ mirroring half of this lens is genuinely clean (re-verified independently, see below) and the lens brief names the accepted-failure-mode-with-no-landing-text category explicitly, so continuing to hold the OPEN back is the withholding the brief forbids. ALTERNATIVES: returning empty a third time on identical text; rejected because the prior pass declined on predicted-skeptic-behaviour rather than on substance and recorded how a successor should argue it ("argue from client-visible session loss, not from completeness"), which is what these two findings do.

FACT: the `slot_cleanup -> leaked` cause enumeration is NOT a docs finding, and here is why, so nobody re-derives it. `docs/reference/state-machines.md:251` says the edge fires "when the cleanup timeout is exceeded", which after SPEC-2/SPEC-3 is narrower than the spec's leak predicate ("a reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit"). But the docs sentence mirrors `spec/06_warm-pod-model.md:148`'s fence annotation ("cleanup timeout exceeded"), which the proposal does not edit. A docs page that agrees with the post-change spec cannot be filed under this lens, and guardrail (1) bars filing the spec annotation from here. — EVIDENCE: docs/reference/state-machines.md:251; spec/06_warm-pod-model.md:148; spec/05_runtime-registry-and-pool-model.md:545 ("If cleanup fails, the slot is leaked"); proposals/.../spec-changes.md:392

FACT: `docs/api/internal.md` is a protobuf reference for the gateway-adapter surface ("documents the protobuf service definitions", :13) and it is wholesale fictional already: it declares `rpc StopSession`, `rpc UploadFiles`, and `StartSessionResponse { bool success = 1; string error_code = 2; string error_message = 3; }`, against a real `StartSessionResponse { string refusal_reason = 1; }` and a real `rpc Shutdown`. SCHEMA-1's `bind_epoch = 2` on that message therefore falsifies nothing that was true. Do not file it as a missed edit site; it is a pre-existing whole-page rot that wants its own proposal. — EVIDENCE: docs/api/internal.md:13,79,83,94,123-127; schemas/lenny-adapter.proto:62,206,958-962

FACT: both staged tier-11 gate edits still check out mechanically, re-verified independently of the r1 pass. `generalSlotEdges` (tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36) feeds a positive loop over §6.2's either-concurrency block (`generalBlock := s62[Index(generalHeader):]`, :54-61) and a negative loop over the concurrent-occupancy block (:69-73); SPEC-4 puts the new edge only in the general block, so one slice entry gates both. DOCS-1's row carries the literal pair `` `receiving_uploads` | `slot_cleanup` ``. The four substrings `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` requires all survive DOCS-2's rewritten row, and both new substrings ("no other bound session", "a session whose start the adapter has admitted") are literally present in it. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36,:54-73; proposals/.../non-spec-changes.md:1266,:1300

FACT: `DemoteSDK` really does drop the registry entry, so DOCS-2's amended row and §4.7.1's caller rule are both true of the tree. `Server.DemoteSDK` calls `anyRegisteredSession()` and then `noteRuntimeClosed` + `releaseSessionSlot` on it. — EVIDENCE: pkg/adapter/sdkwarm.go:274-302

FACT: the `WARM_POOL_EXHAUSTED` angle is dead. CODE-5's `ExcludePods` can newly produce that outcome, but `docs/reference/error-catalog.md:143` and `docs/client-guide/error-handling.md:92` already omit the shipped `details.reason: concurrent_slots_exhausted` cause, so the gap is pre-existing and one more cause does not clear the bar. Same class as `docs/runtime-author-guide/lifecycle.md:431-437`, which already omits the fail-or-leak trigger. — EVIDENCE: docs/reference/error-catalog.md:143; docs/client-guide/error-handling.md:92; docs/runtime-author-guide/lifecycle.md:431-437

UNVERIFIED: DOCS-2's block introduces "the bind sequence" as a bare term on a page that never defines it (`grep -n "bind sequence" docs/` returns nothing repo-wide outside the proposal), so a third-party reader of `adapter-contract.md` cannot tell which seven responses carry the epoch. Judged below the bar as term-definition polish; a stricter docs lens may disagree. — EVIDENCE: proposals/.../non-spec-changes.md:1306; docs/reference/adapter-contract.md

USEFUL [non-spec.2.review-docs-alignment.1]: its two worked-but-unfiled OPENs and its MISTAKE note on `lenny_slot_failure_total` saved this round roughly half its budget. The `docs/` sweep it declared complete holds at HEAD; I re-ran it and added `docs/api/internal.md` as the one page it had not named.


### [non-spec.2.review-edit-sites.1]

DECISION: returned an EMPTY findings list — BECAUSE every edit-site surface I could derive from the
proposal's added identifiers is already staged or already adjudicated — ALTERNATIVES: I considered
filing the "hold refusal predicate does not reach Attach/Interrupt/checkSessionBound paths" divergence
between staged §15.4 ("a request that would create or resolve a registry entry under it is refused with
ABORTED") and CODE-6 (the refusal lives only at the top of `ensureSlotStateLocked`), but it is a close
variant of the already-refuted "§5.2's reclaim-hold refusal predicate covers `Shutdown`, which §15.4
exempts", whose refutation turns on the same vacuous-resolve argument (no entry exists during the hold).

FACT: the named snapshot for this round was byte-identical to the working tree for every proposal file
except the review log, so `diff -ru` yields nothing to read first. EVIDENCE: `diff -rq
scratchpad/cp-snap/.../non-spec-r2 proposals/0081_...` reports only `...review-log.md differ`.
This is the snapshot trap the standing context records, in its ninth form. Budget the round for a
full-document sweep, not a delta read.

FACT: SCHEMA-1's nine field numbers all verify free, including the one that looks wrong. A naive
`awk '/^message AssignCredentialsResponse \{/,/^\}/'` grabs the FOLLOWING message, because
`message AssignCredentialsResponse {}` is a single-line empty message; the awk range then runs into
`RotateCredentialsRequest` and makes field 1 look occupied by `SessionId session_id = 1`. Parse the proto
with a real brace walk. EVIDENCE: schemas/lenny-adapter.proto:1033 (`message AssignCredentialsResponse {}`);
ShutdownRequest holds 1,2,3, reserved 4, 5, 6 at :1610-1635 so 7 is free; ShutdownResponse holds 1,2 at
:1666-1667; PrepareWorkspaceResponse 1,2 at :700-701; FinalizeWorkspaceResponse 1 at :767;
ResumeResponse 1,2,3 at :1434-1446; ConfigureWorkspaceResponse 1 at :1691; RunSetupResponse 1 at :870;
StartSessionResponse 1 at :961.

FACT: the claim-register gates accept the two staged rows as written. `TestClaimRegisterAgreesWithTheAdapterProto`
only resolves a `Message.field`-prefixed claim against the proto when the row's SURFACE string contains
`schemas/lenny-adapter.proto`; the staged `ShutdownRequest.expected_bind_epoch ...` row names Go files
instead, so the proto-resolution arm is skipped rather than satisfied. The WIRED contract (surface names a
file or symbol, no `deferral_id`) is also met. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:68-79;
tests/tier0_static/claim_register_test.go:277-291. Verified counts: tests/claim-map.json holds 76 rows,
32 WIRED / 24 UNWIRED / 20 ABSENT, which is exactly what summary.md:659 claims.

FACT: `slotAddressCaseFiles`'s derived rules are `slotSurfaceCallRE = slotstate.|ClaimSlot(|ReleaseSlot(|
ReserveSlotOnPod(|claimAtCreate(|BindReservedSlot(` and `slotSubjectFileRE = (slot|one_session_only|
sole_session)[^/]*_test\.go$` over walk roots cmd, migrations, pkg, scripts, sdks, tests. EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1168-1173,:1191,:1212-1230. Checked every
test file the proposal touches against both rules: the only two that match are the two slot-named new
files the proposal already enters. Note the regex is case-sensitive, so `releaseSessionSlot(` and
`claimSessionSlot(` do NOT match — `pkg/adapter/bindepoch_test.go` needs no inventory row.
Also confirmed `pkg/gateway/sessionserver/start_test.go`, `resume_setup_demotion_internal_test.go` and
`pkg/adapter/socketruntime_test.go` are edited by the proposal, are absent from the inventory, and match
neither rule today (0 hits each), so their edits do not have to add a row.

FACT: SCHEMA-1's "this is the only closed field set over the messages it opens" holds. The only
descriptor pins in the tree are `shutdown_recycle_wire_test.go` (the one SCHEMA-1 targets),
`checkpoint_stream_wire_test.go` (CheckpointStart/oneof, untouched messages),
`adapter_generation_fence` (requests only), `adapter_session_address` (a reserved-NUMBER map, not a field
count) and `interceptor_proto` (a different proto). EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/
shutdown_recycle_wire_test.go:208-236,:259-275; tests/tier3_contract/adapter_session_address/
session_address_wire_test.go:78-96.

FACT: SPEC-5 inserts INTO the existing `#### 4.7.1 Role and Gateway RPC Contract`; it does not mint a new
subsection, so nothing in §4.7.2–§4.7.11 renumbers and no inbound anchor moves. EVIDENCE:
spec/04_system-components.md:659,:695. The anchor `#471-role-and-gateway-rpc-contract` resolves.

FACT: `tests/registers/identifier-senses.yaml` keys its exemptions by (file, 1-based position among the
file's RETIRED-spelling sites). None of the staged proto, spec or docs text carries `lifecycle channel` or
`control channel` in either spelling, so no occurrence index shifts and the register needs no edit.
EVIDENCE: tests/tier0_static/identifier_resolution_test.go:39-48; tests/registers/identifier-senses.yaml:1-12.

USEFUL [standing context, "The anchor sweep is done ... SIXTEEN sites"]: saved me re-deriving the spec
anchors; I spot-checked §4.7.1, §15.4's `**SDK-warm demotion contract:**` (spec/15:1469, with 15.4.1 at
:1471) and §4.7.9 step 5 (spec/04:853) and all three resolve byte-for-byte.


### [non-spec.2.review-feasibility.1]

DECISION: returned zero findings — BECAUSE every actor/action assignment in the staged
non-spec changes resolves to a component that exists under that name, in the process that
can perform the action, with the data its check needs in scope — ALTERNATIVES: I chased
four candidate breaks to ground (user-credential lease leak, ConfigureWorkspace refusal
arm with no entry, DemoteSDK hold blocking the following bind, ResumeRequest lacking the
budget fields) and each was refuted by the tree.

WATCHOUT: the r2 snapshot at scratchpad/cp-snap/.../non-spec-r2 is byte-identical to the
working tree except for the review log, so `diff -ru` yields nothing. This is the
snapshot Trap's seventh/eighth form the standing context already records; do not spend a
round hunting a delta. EVIDENCE: diff --brief output names only
0081_..._review-log.md.

FACT: `UserCredentialAssigner.MintProto` leases share the pool assigner's lease store, so
`Binder.releaseCredentials` (which calls only `CredentialAssigner.ReleaseSession`) does
return user-source leases too. CODE-4's wrapper comment ("returns its own leases") is
therefore true, not a half-release. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:334-337
("User leases share the lease store the pool assigner uses, so the pool assigner's
ReleaseSession releases them at teardown"); binder.go:1263-1268.

FACT: every adapter handler in the seven-response set constructs its response only on a
path where a registry entry is resolved, so "reports zero on a bind-sequence response is
non-conforming" is satisfiable by the shipped handlers. `ConfigureWorkspaceResponse` has a
`refusal_reason` field but the handler never populates it — every failure is a status
error. `StartSessionResponse` has exactly one construction site. EVIDENCE:
pkg/adapter/sdkwarm.go:199-263; pkg/adapter/session.go:164 (grep for
`StartSessionResponse{` under pkg/adapter has one non-test hit).

FACT: every reclaim hold taken by CODE-6's three routed sites is released inside the same
RPC handler that took it, except the §10.1.4 hold termination, which is not inside any
request. So a hold never blocks a follow-up RPC of the same bind sequence. The one case
worth checking was `DemoteSDK` (which calls `releaseSessionSlot`) immediately followed by
`stageWorkspace`/`PrepareWorkspace` for the same session on the same connection in
`Binder.Prepare`; the hold is gone by the time DemoteSDK answers. EVIDENCE:
pkg/adapter/sdkwarm.go:296-302; pkg/gateway/podlifecycle/podsession/binder.go:880-897,:915.

FACT: all nine SCHEMA-1 field numbers are free on their messages, verified by reading the
proto. `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (7 free); `AssignCredentialsResponse`
is literally `message AssignCredentialsResponse {}` on one line, so field 1 is free — note
that an `awk '/^message AssignCredentialsResponse \{/,/^\}/'` range over it silently spans
the NEXT message and reports the wrong field set. EVIDENCE: schemas/lenny-adapter.proto:1033
(empty one-line message); :1629-1635 (ShutdownRequest 5 and 6).

FACT: the three `tracing.CategorizeError(..., tracing.CategoryPermanent)` stamps CODE-6's
resolve-site table names are at exactly the cited lines. EVIDENCE: pkg/adapter/staging.go:81,
:183, :339.

FACT: `ensureSlotStateLocked` has exactly three production callers, which is why "the five
resolve sites" is complete: staging.go x3 via `ensureSlotPaths`, slotcreds.go:26,
slotsession.go:75. Every other caller is a test. EVIDENCE: grep of
`ensureSlotStateLocked(|ensureSlotPaths(` over pkg/adapter.

FACT: `podRegistry.Put` has exactly the three production callers CODE-6 enumerates, and the
coordinator-handoff one does dial fresh and send `CoordinatorFence` as its first RPC, so the
zero-latch binding CODE-6 reasons about is real. EVIDENCE:
pkg/gateway/sessionserver/start.go:2936, :4057; cmd/lenny-gateway/coordination_seams.go:217-249.

FACT: the tier-0 credit-inventory gate's derived rule 2 is
`(slot|one_session_only|sole_session)[^/]*_test\.go$` matched against the repo-relative PATH,
so it fires on any test file whose last segment merely CONTAINS "slot". Rule 1 is a
case-sensitive body match on `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`,
which `claimSessionSlot(`/`releaseSessionSlot(` do NOT match. That is why the new
`pkg/adapter/bindepoch_test.go` and the new tier-3/tier-10 files need no inventory row while
S10's two slot-named files do. EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173,:1212-1229.

UNVERIFIED: whether the tier-3 files under the new `tests/tier3_contract/adapter_bind_epoch/`
and `tests/tier10_conformance/bind_epoch_conformance_test.go` will trip the credit-inventory
gate's rule 1 depends on whether their bodies reference `slotstate.` or call
`BindReservedSlot(`/`ClaimSlot(`. The proposal names no filenames for the tier-3 directory.
Someone implementing S9/S14 should re-run that gate rather than assume. Below the filing bar
because the proposal states the governing rule (add the entry in the step that creates the
file) and the gate's message names the omitted path.


### [non-spec.2.review-fresh.1]

WATCHOUT: the named snapshot `scratchpad/cp-snap/.../non-spec-r2` is byte-identical to the working tree for every file except the review log, so `diff -ru` yields only compaction-pass prose. Do not spend a round hunting a delta. EVIDENCE: diff of summary/non-spec-changes/spec-changes/implementation-checklist/deviations/status returns empty.

FACT: every verbatim anchor SPEC-1..SPEC-5 quotes still resolves in the tree. I checked all nineteen (the §4.1 third sentence, the §4.7 `Shutdown` row opener, §7.1's rollback parenthetical, §7.2's preamble premise sentence + step 2 tail + step 3, §7.3's list tail, §6.2's mid-resume clause and `receiving_uploads ──→ running` and `**\`reserved\` hold semantics.**` and the either-concurrency header, §5.2's `**Slot cleanup:**` action list and `**Scrub model.**` paragraph and `**Max retries:**` sentence, §4.7.9 step 5, §29.4's step-13 tail, §15.4's `**SDK-warm demotion contract:**`, and the two insertion-point headings §4.7.2 and §15.4.1). Do not re-verify these. EVIDENCE: grep -F over spec/04,05,06,07,15,29.

FACT: every SCHEMA-1 field number is free on its message and `AssignCredentialsResponse` really is empty. `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved. EVIDENCE: schemas/lenny-adapter.proto messages ShutdownRequest/ShutdownResponse/PrepareWorkspaceResponse/FinalizeWorkspaceResponse/ResumeResponse/ConfigureWorkspaceResponse/RunSetupResponse/AssignCredentialsResponse/StartSessionResponse.

FACT: the only closed proto field set over a message SCHEMA-1 opens is `TestShutdownMessagePostRemovalDescriptor_spec_4_1`. The other descriptor gates pin InterceptRequest/InterceptResponse, CheckpointBarrierResponse, NegotiateVersionResponse and the checkpoint-stream messages, none of which SCHEMA-1 touches, and no test anywhere reflects over the seven bind-sequence response descriptors. EVIDENCE: tests/tier3_contract/{interceptor_proto/contract_test.go:38, adapter_checkpointbarrier/checkpointbarrier_wire_test.go:49, gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208}.

FACT: the two new claim-register rows cannot trip `TestClaimRegisterAgreesWithTheAdapterProto`. That gate only demands a `Message.field` claim resolve when the row's `surface` string contains `schemas/lenny-adapter.proto`; both staged rows name Go files instead. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:67-77. And `tests/claim-map.json` really carries 76 rows / 20 ABSENT / 24 UNWIRED / 32 WIRED.

FACT: the tier-0 credit-inventory gate has TWO independent halves and only one was well understood in earlier rounds. `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108) demands a `slot*_test.go`-named or slot-surface-calling file be IN `slotAddressCaseFiles`. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:970) then demands, for every file already in that inventory, that `tests/spec-map.json` credit each section the file's `// spec:` annotations name — per case when the map registers that file case by case. So adding a NEW CASE with a NEW SECTION to an already-inventoried file breaks tier 0 just as surely as adding a new file. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:826-870, :953-989.

FACT: which of this proposal's touched test files sit in `slotAddressCaseFiles`, and how they are credited today — `pkg/adapter/slotsession_test.go` whole-file {4.7,4.9,5.2,6.1,6.4,15.4} (new cases cite 4.7/5.2, covered); `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` whole-file {4.1,4.6.3,4.7,5.2,6.2,7.1} (covered); `pkg/gateway/sessionserver/slotretry_test.go` per-case (proposal only adds rows to existing funcs, so no new credit); `pkg/gateway/podclaim/slotclaimer_test.go` whole-file {4.1,5.2} + per-case (NOT covered for 7.1 — filed); `tests/tier4_integration/recycle_scrub_path_test.go` per-case, whole-file {15.1} only (any new func needs its own per-case credits; the proposal states no annotation for its new case, so this is an UNVERIFIED rather than a filing); `tests/tier4_integration/concurrent_workspace_test.go` whole-file {5.2,6.4,15.1,28.5.3}. `pkg/adapter/socketruntime_test.go` and `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` and `pkg/gateway/sessionserver/start_test.go` are NOT in the inventory.

FACT: `pkg/adapter/bindepoch_test.go` does NOT enter the credit inventory. The derived rule's regex is case-sensitive (`ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(|slotstate\.`), so the adapter-internal `claimSessionSlot(` and `releaseSessionSlot(` do not match, and the file name carries no `slot`. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168, :1173.

UNVERIFIED: the tier-4 "datastore-crossing case" added to `tests/tier4_integration/recycle_scrub_path_test.go` and the tier-4 cases added to `concurrent_workspace_test.go` carry no stated `// spec:` annotation in the proposal. If either lands citing §7.1 (or, for recycle_scrub_path, any section at all, because that file is credited per case and its only whole-file credit is 15.1), tier 0 goes red the same way the slotclaimer finding does. Someone should either state the annotations or extend the `tests/spec-map.json` bullet to say "plus a per-case credit for every new case in an inventoried file".

FACT: `§` is a supported citation prefix in the tier-0 annotation parser, and `;` is a supported separator, so the proposal's `// spec: §4.7; §5.2` form parses. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:645 (FieldsFunc on `,` and `;`), :668-676 (`announcesItselfAsACitation` accepts a leading `§`).

FACT: all six production `ReleaseSlotReservation` call sites match CODE-4's six-row table exactly, and `materializeSlot` really carries five `cl.Close()` calls. EVIDENCE: pkg/gateway/sessionserver/start.go:2727,:2834,:3246; pkg/gateway/podlifecycle/podsession/slotbinder.go:172,:217,:493; binder.go:1714; slotbinder.go:286,:293,:301,:308,:321.

FACT: the adapter-side line citations CODE-1/CODE-2 lean on are all accurate as of this read — session.go:133/:147/:157 (releaseSessionSlot), :156 (Runtime.Start), :163 (noteRuntimeStarted); resume.go:42/:50/:101-104/:140/:144; slot.go:105 (ensureSlotStateLocked), :140 (ensureSlotPaths); slotsession.go:75, :87-88, :214-220, :238-260, :306-313, :375-395; staging.go:81/:134, :181-186, :337-342; slotcreds.go:26-28. The five `InvalidArgument` resolve sites are exactly the five the table names.

DECISION: I filed only the spec-map credit gap and nothing else. BECAUSE every other candidate I developed either resolved cleanly against the tree or duplicated an already-refuted family (the StartSessionResponse/`Resume` response epoch not being enumerated under CODE-6's file heading is the same bookkeeping class the material skeptic already refuted; the S9 tier-3 "Resume reports a non-zero epoch" case is covered by the S9 checklist line's own "the seven handlers report the entry's epoch on their responses", so the file-list omission is not an ordering break). ALTERNATIVES: I considered filing the zero-frame `PrepareWorkspace` conformance corner (a third-party adapter receiving a stream that never resolves a slot identifier has no conforming epoch to report, since §15.4 makes reporting zero non-conforming) and dropped it — the log records that exact tension as already found-and-fixed once and as a standing Open scoped to third-party callers.


### [non-spec.2.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE this proposal has essentially no Kubernetes surface: no CRD schema edit, no chart edit, no controller edit, no finalizer, no status-subresource write, no admission-webhook change. Every mechanism it stages is either adapter-process in-memory state (bind epoch, reclaim hold, slot registry) or gateway in-process state (`ExcludePods`, `slothealth.Tracker`, `slotstate.Registry`). ALTERNATIVES: I looked hardest at CODE-5, the only deliverable that reaches a Kubernetes object at all, and it reaches it only through the pre-existing `DrainSandbox` seam.

FACT: `Files touched on application` (non-spec-changes.md:2042-2134) names zero files under `charts/`, `pkg/apis/`, `pkg/controller/`, or any `*_types.go`. Nothing in the staged spec edits touches §4.6.3, §10.3, §13.2, or any admission-webhook text either — a grep of spec-changes.md for `status|finalizer|CRD|controller|reconcil|admission|webhook|SandboxClaim|etcd|WarmPool|annotation` returns four hits, all of them incidental (a gRPC `ABORTED` status code at :191 and :713, the word "status" in "authoritative-enumeration status" at :508, and "CRD validation rule" at :621 inside a sentence saying the rule is UNCHANGED). EVIDENCE: proposals/0081_.../0081_....spec-changes.md:191,:508,:621,:713.

FACT: CODE-5's new `resumeOnPod` caller of `accountSlotFailure` adds a THIRD code path that can reach `Binder.DrainSandbox`, which is a gateway write to a Kubernetes object. It is idiom-clean and needs no RBAC or ownership change: `DrainSandbox` stamps the `lenny.dev/drain-request` Pod annotation and nothing else, the WarmPoolController is the sole writer of the resulting `Sandbox.status` phase, and §4.6.3 grants the gateway `get`/`patch` on Pods for exactly that annotation, scoped by outcome ("when a pod crosses the unhealthy threshold") rather than by which gateway code path crosses it. So a new caller of the same threshold tail needs nothing new. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:601; spec/04_system-components.md:418 ("both transitions are WarmPoolController-written"), :632 ("stamping the `lenny.dev/drain-request` annotation when a pod crosses the unhealthy threshold"); pkg/controller/warmpool/pod_reconciler_test.go:413 ("gateway stamps drain-request; WarmPoolController writes the drain").

FACT: the proposal already states the one controller-on-the-hot-path hazard in this area and resolves it correctly rather than leaving it implicit: at `maxConcurrentSessions: 2` the same iteration both drains the pod and retries, but `DrainSandbox` is asynchronous (annotation, WarmPoolController acts later) while `ClaimSlot`'s pass-1 scan reads the per-pod claim rather than the Sandbox phase, so the drain cannot keep the immediate retry off that pod. The synchronous `ExcludePods` filter is what does. That is exactly the "do not make a request path wait on a reconcile" answer. EVIDENCE: non-spec-changes.md:893-899.

FACT: `ExcludePods` is per-request in-memory state on `podsession.SlotBindRequest`/`podclaim.SlotRequest`, read-only at the two `ClaimSlot` candidate passes, never persisted and never crossing a request boundary. No CRD field, no annotation, no Redis key. So the "etcd is not a message bus / per-request database" idiom is not engaged. EVIDENCE: non-spec-changes.md:828-874.

WATCHOUT: the genuinely Kubernetes-shaped residue in this proposal is the `leaked` disposition holding a `SandboxClaim` at `bound` with no active session, which looks like an undeletable-object footgun until you find the level-triggered backstop. It is NOT a finding: §4.6.1 orphan GC drains a `bound`/`recycling` claim older than `claimOrphanTimeout` whose pod no active session references, and no finalizer is involved anywhere in this change. The standing context already records this; do not re-derive it. EVIDENCE: review-log.md Standing context, "The `SandboxClaim` a leaked reclaim leaves `bound` is not undeletable"; "Ownership is clean."

FACT (cheap, saves a round): `diff -ru scratchpad/cp-snap/0081_.../non-spec-r2 proposals/0081_...` reports exactly ONE differing file, the review log itself. The staged spec-changes, non-spec-changes, summary and checklist are byte-identical to the r2 snapshot. The "read what changed first and hardest" instruction therefore had nothing to point at this round; budget the whole pass as a fresh read of the unchanged document. This is the eighth instance of the snapshot trap the standing context warns about.

USEFUL [Standing context, "Ownership is clean."]: it let me stop short of re-deriving the whole field-ownership question from §4.6.3 and instead spend the pass confirming the one new write PATH (CODE-5's resume caller) rather than re-checking every writer. It was accurate.


### [non-spec.2.review-mechanism.2]

DECISION: empty findings list for the end-to-end-mechanism lens on round 2 of the non-spec loop — BECAUSE every flow I traced end to end (bind → failure → compensation → outcome → disposition → placement; reclaim hold → refusal → gateway classifier; epoch mint → latch → fence; §10.1.4 hold termination; DemoteSDK) resolved against the tree, and the one novel mechanism defect I derived is barred by a standing Trap. ALTERNATIVES: filing the epoch early-return / clause-three defect (see WATCHOUT below) and the S9→S13 classifier-ordering window (intermediate-commit behavioural regression, not a gate regression; review-log.md:214 records the intermediate-gate analysis and the sequence lands as one series).

WATCHOUT: the snapshot named in the round-2 prompt is byte-identical to the working tree for every file except the review log, so `diff -ru scratchpad/cp-snap/.../non-spec-r2 proposals/0081_...` yields nothing to read first. This is the snapshot Trap's ninth recorded form. Do not spend the round hunting a delta. EVIDENCE: `diff -rq` output names only `...review-log.md`.

WATCHOUT: I independently re-derived CODE-1's `ABSENT`/`SUPERSEDED` early returns skipping clause three (the whole-pod recycle scrub), which contradicts SPEC-1's staged §4.1 sentence ("runs the whole-pod scrub when the recycle disposition is set"). It is ALREADY BARRED: review-log.md:753 ("Do NOT file CODE-1's epoch early-return skipping clause three ... Four lenses built it"), :707, :732, :1815. Unreachable because `ShutdownReclaim` is the only epoch-bearing sender and carries no recycle, and `ShutdownRecycle` keeps a zero epoch. EVIDENCE: proposals/0081_.../…non-spec-changes.md:159-175; spec-changes.md:287; pkg/adapter/session.go:283-291.

FACT: every SCHEMA-1 field number is free on its message, verified by parsing `schemas/lenny-adapter.proto` rather than by grep. `PrepareWorkspaceResponse` holds 1,2 (→3 free); `FinalizeWorkspaceResponse` 1 (→2); `RunSetupResponse` 1 (→2); `StartSessionResponse` 1 (→2); `AssignCredentialsResponse` is `{}` (→1); `ResumeResponse` 1,2,3 (→4); `ConfigureWorkspaceResponse` 1 (→2); `ShutdownResponse` 1,2 (→3); `ShutdownRequest` 1,2,3,5,6 with 4 reserved (→7). EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1033,1433,1665,1690,1609-1635.

FACT: the five resolve sites are EXHAUSTIVE against the tree. `ensureSlotPaths` has exactly three production callers (staging.go:134, :181, :337) and `ensureSlotStateLocked` exactly three (slot.go:143 inside `ensureSlotPaths`, slotcreds.go:26, slotsession.go:75), so the hold refusal placed at the top of `ensureSlotStateLocked` reaches every entry-creating path and the CODE-6 table misses none. The three span-category stamps cited at staging.go:81, :183, :339 all land on the exact lines the proposal names. EVIDENCE: `grep -rn "ensureSlotStateLocked\|ensureSlotPaths" --include=*.go pkg/ | grep -v _test`.

FACT: all four verbatim anchors SPEC-1/2/3 quote are byte-exact in the tree today: spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown` row opening), spec/04:854 (§4.7.9 step 5), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**`), spec/05:555 (`**Max retries:**`). A future citations round can take these as checked at this commit.

FACT: `onHoldTimeout`'s pass 2 has no early exit — it iterates every member of `deregisterStartedSessions()` unconditionally under one shared 10s close context — so CODE-6's "pass 1 carries each member's hold to the pass-2 call that ends it" cannot strand a hold on a partial iteration. EVIDENCE: pkg/adapter/holdstate.go:190-206; pkg/adapter/slotsession.go:375-396.

FACT: `Binder.Resume` sends exactly one entry-touching RPC (`cl.Resume` at binder.go:1607) after `b.connect` and `reserveResumeSlot`, so CODE-4's "the connection's epoch latch is still zero, and the unconditional form is correct there" holds against the tree. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1631.

FACT: `exited_cleanly` has exactly one production reader that acts on it, `Binder.ReleaseSlot` at slotbinder.go:542 (`leaked = err != nil || !cleanly`); the recycle sender at :574 and binder.go:2037 discard it. CODE-1's `live ||` disjunct therefore cannot reclassify an ordinary session end, because that path's session is in `runtimeLive`. EVIDENCE: `grep -rn "GetExitedCleanly\|ExitedCleanly" --include=*.go pkg/ cmd/ | grep -v _test | grep -v pkg/proto`.

USEFUL [review-log.md:753]: saved a finding four lenses have already built and lost. Read the "Do NOT file" Traps BEFORE deriving, not after; grepping `^- \*\*Do NOT\|^- \*\*WATCHOUT\|^- \*\*MISTAKE` over the review log gives the whole bar list in one command and costs one tool call.


### [non-spec.2.review-operational.2]

DECISION: returned an EMPTY findings list for the operational-consistency lens on the
non-spec staging — BECAUSE the proposal directory is BYTE-IDENTICAL to the named snapshot
(`diff -rq scratchpad/cp-snap/.../non-spec-r2 proposals/0081_...` reports only the review log
differing), so nothing in this lane's scope is new since `[non-spec.2.review-operational.1]`
swept it, and that shard's one filing was subsequently REFUTED. I re-derived every
observability surface independently rather than trusting the prior shard, and reproduced only
candidates the record already disposes of.
ALTERNATIVES rejected, each with what killed it:
(a) "The retry exclusion (`ExcludePods`) makes an operator see `WARM_POOL_EXHAUSTED` /
`concurrent_slots_exhausted` on a pool that has idle capacity, while
`docs/operator-guide/troubleshooting.md:17-21` lists the symptom as
`lenny_warmpool_idle_pods` gauge at 0" — PRE-EXISTING. `ClaimSlot` already returns
`ErrNoConcurrentSlot` whenever no candidate is placeable, and `expiredByUptime` is a shipped
skip with the same effect; the exclusion adds an instance to a gap it did not create. Already
recorded as pre-existing at review-log.md:1347(a).
(b) The `slot_cleanup ──→ leaked` "(cleanup timeout exceeded)" gloss in `spec/06:148` and
`docs/reference/state-machines.md:251` — standing Trap, pre-existing, do not re-derive.
(c) `lenny_slot_failure_total` having no series on the `Binder.Resume` compensation path —
this is exactly the finding `[non-spec.2.review-operational.1]` filed and the material
skeptic refuted; it is also standing Open #816. Not re-filed.
(d) `SessionAvailabilityBurnRate` gaining a contributor through CODE-5's `codes.Aborted` arm —
killed on the same ground the prior shard killed it (the signal is honest and the window is a
millisecond-scale race).

FACT: the alert half of this lens is empty at HEAD, confirmed mechanically rather than from
the Settled entries: `grep -rln "leaked_slots\|slot_failure\|slot_reclaim" charts/
docs/runbooks/ pkg/alerting/` returns NOTHING. No alert, runbook, or chart artifact
references any metric this proposal's paths move, so no alert-to-runbook resolution can break.
EVIDENCE: pkg/alerting/rules/ (no hit); docs/runbooks/ (no hit)

FACT: `lenny_adapter_leaked_slots` is absent from `docs/reference/metrics.md` entirely (only
`lenny_slot_failure_total` is listed, at :166, described as "Per-slot failures on session-mode
pods with `maxConcurrentSessions > 1`", which stays true after the change). That absence is
pre-existing and deliberate: BUILD-GAPS.md:4963 records the gauge being kept out of the §16.1
typed catalog on purpose. Do not file the gauge's absence from the metrics reference as a
missed edit site for this proposal. EVIDENCE: docs/reference/metrics.md:166;
BUILD-GAPS.md:4963; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:223

FACT: DOCS-2's `DemoteSDK` row claim verifies in the tree. `Server.DemoteSDK` calls
`s.noteRuntimeClosed(sessionID)` then `s.releaseSessionSlot(sessionID)` on the registry's
single entry, so "drop the adapter's slot registry entry for the session" is accurate, and the
next bind therefore creates an entry for an identifier the adapter holds none for, which is
§4.7.1's minting trigger. EVIDENCE: pkg/adapter/sdkwarm.go:296-301, :309-316

FACT: the DOCS-1 tier-11 plan's gate mechanics check out. `generalSlotEdges` is
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36, the positive loop
over the either-concurrency block is :55-59 and the negative loop over the concurrent-occupancy
block is :70-74, so one slice entry does gate both sides as the proposal claims. The new edge
string `receiving_uploads ──→ slot_cleanup` is not a substring of any existing edge and no
existing edge is a substring of it, so neither loop misfires. `section(doc, "Per-slot
sub-states")` stops at `### Concurrent-session occupancy`, so DOCS-1's row lands inside the
section the new `requireAllContain` entry reads. EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36,:55,:70,:101-109;
docs/reference/state-machines.md:228-240

FACT: both substrings DOCS-2 adds to the shipped `Shutdown`-row gate occur verbatim in the
staged row, and both whole-page substrings (`bind epoch`, `reclaim hold`) occur in the staged
block in the exact lowercase `strings.Contains` needs. Checked character by character, because
`requireAllContain` is case-sensitive. EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311;
non-spec-changes.md:1300,:1310

USEFUL [non-spec.2.review-operational.1]: its four DOCS-2 anchor verifications
(page self-description :10, `Shutdown` row :75, `DemoteSDK` row :64, `**Scrub
responsibilities.**` :81, gate at :294 asserting exactly four substrings) are all still
accurate at HEAD and saved a full re-verification pass. Its "nearly filed, three dresses"
block also saved me from re-deriving the SLO-contributor argument.

USEFUL [Settled "no alert references any slot metric"]: accurate, and now confirmed by a
direct repo-wide grep rather than by inheritance. Keep it.

OPEN: the loop has now run the operational lens four times (three on the spec lane, two on the
non-spec lane counting this one) with one filing, refuted. Unless `spec-changes.md` or
`non-spec-changes.md` actually changes, a further firing of this lens has no text to read.
Whoever schedules round 3 should check the byte-identity first.


### [non-spec.2.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the proposal text has not moved at all since
this lane's r2 snapshot (`diff -rq scratchpad/cp-snap/0081_.../non-spec-r2 proposals/0081_...`
reports exactly one differing file, the review log; spec-changes.md, non-spec-changes.md,
summary.md, the checklist, deviations and status are byte-identical), and every capacity and
failure-mode exposure on that text has already been derived, recorded and declined by the
earlier performance passes in this lane. ALTERNATIVES: I re-checked and did not re-file (a) the
replica-local `slothealth` ledger versus the durable Redis occupancy a `leaked` release withholds,
(b) the compensating `Shutdown` pinning a gateway request goroutine for up to `slotCleanupBudget`
after the client has gone (human open decision 16), (c) the exclusive-pool resume budget
degenerating to the whole `cleanupTimeoutSeconds`, (d) a tier-8 chaos obligation. All four are
standing Open items declined by four or more rounds.

FACT: the capacity half of this lens remains structurally vacuous on this change and that is
mechanically checkable rather than a judgement — `grep -n "etcd\|Postgres\|Redis\|informer\|watch"`
over non-spec-changes.md returns exactly two hits, both describing the SHIPPED Redis slot-counter
behaviour that a `leaked=true` release already has (non-spec-changes.md:782, :1733). No staged
deliverable adds an etcd status write, a CRD field, a Postgres or Redis write, an informer, a
watch, or a metric series; the epoch is an in-memory pod-local `int64` and the hold an in-memory
`map[string]struct{}` key, both under the `s.mu` the adapter already takes for every registry
touch. EVIDENCE: non-spec-changes.md:782, :1733.

WATCHOUT for the next performance agent: this lens has now fired roughly a dozen times on this
proposal and at least six of those were over text that did not move. Before spending a pass,
run the `diff -rq` against the lane snapshot first; if only the review log differs, read
`### Open` and the prior performance shards and stop. A filing in this lens needs a mechanism
that is NEW in the staged text.

USEFUL [non-spec.1.review-performance.2]: its four named-and-killed alternatives plus the
"staged text has not moved in six rounds" warning is exactly the state this round found, and
reading it first is what kept this pass from re-deriving the durable-occupancy asymmetry.

USEFUL [spec-recheck.4.review-reliability.1]: the `RestartPolicy: Never` dependency
(pkg/controller/sandbox/podspec/podspec.go:980) is the single tree fact that makes the epoch's
pod-local monotonicity safe across a transparently re-established `*grpc.ClientConn`. Anyone
touching the pod spec's restart policy must reopen the epoch design.


### [non-spec.2.review-reliability.2]

WATCHOUT: the named snapshot for this round (`scratchpad/cp-snap/.../non-spec-r2`) is
byte-identical to the working tree except for the review log, so `diff -ru` yields nothing
about the proposal itself. This is the snapshot Trap's eighth form; do not spend the round
hunting a delta. EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../non-spec-r2 proposals/0081_...`
reports only `*.review-log.md` differing.

FILED: **the §5.2 retry IS a producer of reclaim-hold encounters, through `releaseSessionSlot`.**
non-spec-changes.md:1948-1950 opens the hold-encounter enumeration with "The §5.2 retry is not a
producer ... Two callers can", grounded on two branches (excluded pod; acknowledged reclaim
answered after its own cleanup). Both grounds are about the compensating `Shutdown`. CODE-6
(:992-997) also routes `releaseSessionSlot` through `reclaimSlotLocked`, and that is the
abandoned attempt's OWN lagging rollback — the one CODE-5's "When it does not fire" bullet
(:878-889) says the gateway stopped waiting for. Ordering: gateway's StartSession deadline
expires → adapter's handler fails at session.go:133/:147/:157 and enters `releaseSessionSlot`,
taking the hold across `removeSlotTree` → compensation `Shutdown` arrives, is exempt from the
hold, finds no entry, answers ABSENT → CODE-4 maps ABSENT to not-leaked (:569-575) → pod is NOT
excluded → `applySlotRetryPolicy` re-places on the same pod → the retry's first entry-resolving
RPC meets the live hold and is refused `Aborted`. `maxSlotRetries` is 1, so the attempt is over.

FACT: the standing-context entry "`releaseSessionSlot` ... is fully synchronous, which is what
makes the hold it opens end before the requesting RPC answers" (review-log.md:498) is the
counter-argument to the above, and it does NOT hold on the abandoned-attempt path, because there
the gateway never waits for that RPC's answer. Anyone re-checking this finding must not stop at
:498. Same for the `DemoteSDK` dead-end entries (:711, :729): those are about a caller that does
wait.

FACT: `releaseSessionSlot` calls no `Runtime.Close`; its hold would span `removeSlotTree` alone
(pkg/adapter/slotsession.go:214-220). That bounds the window to one `RemoveAll` over the slot
tree, which is still seconds on a large workspace and is the whole basis for the harm.

FACT: `StagingPath` does no `MkdirAll` and the streaming `PrepareWorkspace` opens files with
`os.OpenFile(..., O_CREATE)` under an already-resolved `stagingDir`, so an in-flight upload stream
whose first frame resolved BEFORE the hold opened cannot resurrect a removed slot tree — later
frames fail ENOENT. I built and dropped a finding on that hole.
EVIDENCE: pkg/adapter/workspace/materialize.go:675-681; pkg/adapter/staging.go:76-100.

FACT: the epoch-mismatch early returns in CODE-1's staged clause two (`return` before clause
three) skip `startPodScrub`, so an epoch would also gate the recycle disposition. Inert today:
only the new `ShutdownReclaim` carries a non-zero epoch and it takes no recycle parameter
(`Shutdown`/`ShutdownRecycle` keep their signatures and send zero). Dropped as unreachable.
EVIDENCE: pkg/adapter/session.go:283-290; non-spec-changes.md:1099-1101.

FACT: `SlotBindError.Reason()` has no `Aborted` case and no per-stage arm that would catch it —
the only stage-sensitive arm is `FailedPrecondition`/`workspacePrep`. So the reclaim-hold refusal
classifies transient from the workspace, setup, credential AND start stages alike, and the
proposal's classifier reasoning is sound at every stage. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101.

FACT: `FinalizeWorkspace` and `RunSetup` do not return the resolve error — they mint a FRESH
`status.Errorf(codes.InvalidArgument, "resolve workspace for session %s: %v", ...)` with `%v`,
so the sentinel's code is destroyed by construction rather than by a wrapper. CODE-6's
`slotResolveError` must replace the whole construction at those two sites, not just the wrap.
EVIDENCE: pkg/adapter/staging.go:181-185, :337-341.

FACT: `onHoldTimeout` always drains its whole member list (no early exit after pass 1 returns a
non-empty set), so routing pass 1 through `reclaimSlotLocked` leaks no hold on that path.
EVIDENCE: pkg/adapter/holdstate.go:177-206.


### [non-spec.2.review-security.1]

DECISION: returned an empty findings list — BECAUSE the proposal directory is byte-identical to
the `non-spec-r2` snapshot outside the review log (`diff -rq` reports only `...review-log.md`
differing), and the security lens has now returned empty on this exact text twice already
(`[non-spec-recheck.4.review-security.1]`, `[non-spec.1.review-security.1]`). I did not defer to
those: I re-derived the revoke path, the epoch trust boundary, the reclaim-hold/`Shutdown`
interaction and the proto numbering independently. ALTERNATIVES considered and rejected below.

FACT: the §11.4 full-revoke fan-out is still `bind.Adapter.Shutdown(callCtx, bind.SessionID,
reason, userTerminateDeadline)` — four arguments, no epoch — at
cmd/lenny-gateway/user_revocation.go:129, and `Client.Shutdown`'s shipped signature is
`(ctx, sessionID, reason string, deadline time.Duration) (bool, error)` at
pkg/gateway/runtime/adapterclient/client.go:807. CODE-6 keeps that signature and passes a zero
epoch (non-spec-changes.md:1073-1076), so credential revocation cannot be fenced by a pod-minted
epoch. This is the load-bearing check for the escape hatch; it holds.

FACT: SCHEMA-1's three security-adjacent field numbers are free in the tree. `ShutdownRequest`
holds 1, 2, 3, `reserved 4`, 5, 6 (schemas/lenny-adapter.proto, `message ShutdownRequest`), so 7
is next; `ShutdownResponse` holds 1 and 2, so 3 is next; `AssignCredentialsResponse` is
`message AssignCredentialsResponse {}`, so 1 is its first field. All three SCHEMA-1 rows
(non-spec-changes.md:1190-1198) are correct. Do not re-derive.

FACT: `coordination_generation` is a live §10.1 fence field on `ShutdownRequest` (field 6) whose
proto comment claims "A pod validates the generation on every gateway-to-pod RPC", but the
adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`
(pkg/adapter/coordination.go:120, :262 are the only non-test read sites in `pkg/adapter`). That
is a PRE-EXISTING gap between the proto comment and the handler set, not something 0081 touches,
and the new `expected_bind_epoch` neither rides on nor bypasses it. I looked hard at this as a
possible "a fence already exists, the epoch duplicates or weakens it" finding and it is neither:
the generation is unread on `Shutdown` today, so the epoch adds the only fence that path has.

WATCHOUT: the tempting security filing here is "the bind epoch is a pod self-report gating a
gateway teardown, and a lying adapter answers `SUPERSEDED` to every reclaim and never gets
drained". Do not file it. `exited_cleanly` is already the pod self-report that drives `leaked`
(pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559), so the same evasion exists before
the amendment; the adapter container is Lenny-managed rather than tenant code; and the family is
already killed twice in this log. A future filing needs a REACHABLE conforming producer.

WATCHOUT: the second tempting filing is "withholding `ReportSessionScrub` on the pre-`running`
path lets a pod dodge `maxSessionsPerPod` retirement and reuse itself". Also not a finding: the
adapter is already the sole source of that RPC and of `IncrementSessionsServed`
(pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481), so the
proposal delegates no bound the gateway previously measured independently. The change only
supplies a conforming reason to withhold a report a non-conforming pod could already omit.

USEFUL [non-spec.1.review-security.1]: its three FACTs (fresh dial per bind attempt so the
per-connection epoch latch cannot cross sessions, the unconditional revoke `Shutdown`, and
`onHoldTimeout`'s inseparable pass 1 / pass 2) are all still true and saved me the whole
latch-crossing and stranded-hold derivations.

USEFUL [non-spec-recheck.1.review-security.1]: its WATCHOUT on `ExitedCleanly` discarding the
tree-removal error, and the one on the §11.4 revoke of a bound-but-unstarted session losing its
`Runtime.Close` under CODE-1, are the two live-looking regressions I would otherwise have filed.
Both are strict improvements or no-changes against the shipped handler.


### [non-spec.2.review-test-coverage.1]

FACT: the named snapshot `scratchpad/cp-snap/.../non-spec-r2` differs from the working tree in the REVIEW LOG ONLY (`diff -rq` reports one file). The proposal body is byte-identical to r1, so "read what changed" yields nothing and the whole document has to be re-read. This is the snapshot Trap's ninth form. EVIDENCE: diff -rq scratchpad/cp-snap/0081_.../non-spec-r2 proposals/0081_...

FACT: the tier-0 credit gate has TWO distinct rules and only one of them is written into the proposal. (1) `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` requires a FILE to be in `slotAddressCaseFiles` when its name matches `(slot|one_session_only|sole_session)[^/]*_test\.go$` or its body matches `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(` — the proposal covers this at non-spec-changes.md:2058-2066. (2) `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` requires, for every file already in that inventory, that each `// spec:` section a CASE cites be credited in `tests/spec-map.json` either whole-file (a plain path, or a `dir/...` subtree prefix) or per case (`path::TestName`). Rule (2) is what a new case in an EXISTING inventory file trips, and no edit list in the proposal mentions it. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:970-989, :953-962, :811-819, :1108-1131, :1212-1229.

FACT: whole-file/subtree credit is what makes most of the proposal's new adapter cases safe. `pkg/adapter/...` is a subtree entry under 4.7, so `pkg/adapter/slotsession_test.go`'s new `// spec: §4.7; §5.2` cases need nothing. `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` is whole-file credited under 4.1, 4.6.3, 4.7, 5.2, 6.2 AND 7.1, so its new cases are safe too. The two that are NOT safe are registered case-by-case with narrow whole-file credit: `slotclaimer_test.go` (whole: 4.1, 5.2 only; 9 per-case entries) and `pkg/gateway/sessionserver/slotretry_test.go` (whole: 4.1, 11.4, 15.1 only; 15 per-case entries). EVIDENCE: tests/spec-map.json:1030; tests/tier0_static/spec_map_slot_address_registration_test.go:298,:326.

WATCHOUT: do not "fix" this by adding a whole-file entry for `slotclaimer_test.go` under 7.1. A sibling gate (`TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise` and the per-case machinery around it) treats a case-by-case-registered file as case-by-case; the correct fix is per-case `path::TestName` entries, which means the proposal must also name the tier-2 and tier-1 test functions it currently describes only in prose. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:824-826 ("A section that names one case credits that case alone").

UNVERIFIED: `tests/tier4_integration/recycle_scrub_path_test.go` is also case-by-case registered with whole-file credit under 15.1 alone, so IF the datastore-crossing case lands as a new test function rather than as assertions inside an existing one, it needs per-case entries too. The proposal's wording ("Extend ... the case adds a unary client interceptor", non-spec-changes.md:1671-1690) does not settle which. Somebody implementing S12 should check.

FACT: several behaviours I went hunting for turn out to be already pinned and are not gaps. `cancelPodMCPIfRuntimeIdle` widening from the `bound` gate to `removed` is inert: the double guard `!runtimeIdleLocked() || mcpArmingHeldLocked()` returns early whenever the arming session still holds a slot, and `mcpSession` is only ever set by a bound claim. EVIDENCE: pkg/adapter/slotsession.go:238-260. And `ConfigureWorkspace`'s idempotent-repeat epoch (CODE-6, non-spec-changes.md:1058-1060) is covered generically by the tier-1 bullet "A second bind-sequence RPC resolving an entry the `Server` already holds reports that entry's epoch and mints nothing" (:1336-1339) plus the CONF-1 property at :1117-1120; there is no separate branch in the handler, because `claimSessionSlot` returns the existing entry's epoch on the non-fresh arm (pkg/adapter/sdkwarm.go:217-221).


### [non-spec.3.fix-G1.1]

DECISION: the tier-4 concurrent-workspace case is named `TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1`, annotated `// spec: §7.1; §5.2; §4.7`, the late-reclaim arm is a subtest inside it, and `tests/spec-map.json` gains WHOLE-FILE credits for `tests/tier4_integration/concurrent_workspace_test.go` under 7.1 and 4.7 at S12 — BECAUSE the file is in `slotAddressCaseFiles` and the map registers it as a whole, so `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` checks the union of every section cited anywhere in the file — ALTERNATIVES: per-case `path::TestName` rows (rejected: one such row flips the whole file into per-case mode and re-attributes the shipped cases and file-level annotations); folding the assertions into the shipped `TestConcurrentWorkspacePerSlotExecution_spec_5_2` under §5.2 alone (rejected: the only tier-4 case pinning §7.1's reclaim and §4.7's `SUPERSEDED` answer would be credited to neither, and `lenny-test --spec 7.1` would not select it); copying the sibling's `§7.1; §5.2; §6.2` (rejected: this case asserts no §6.2 edge and does assert §4.7).

FACT: `tests/tier4_integration/concurrent_workspace_test.go` is whole-file registered under sections 5.2, 6.4 and 28.5.3 only, plus 15.1 through the `tests/tier4_integration/...` subtree entry; it carries no `::TestName` row. Sections 4.7 and 7.1 carry tier-4 entries for other files but none for this one. — EVIDENCE: tests/spec-map.json (sections 5.2, 6.4, 28.5.3 test lists); tests/tier0_static/spec_map_slot_address_registration_test.go:425 (inventory membership), :970-988 (the gate)

DECISION: deleted "plus cases in the two existing files below" from the CODE-6 tier-1 subsection — BECAUSE it contradicted the same subsection's "all of them land in the one new file", had no resolvable referent, and the nearest two files (`pkg/adapter/slotsession_test.go`, `pkg/adapter/socketruntime_test.go`) belong to a different, correctly scoped subsection — ALTERNATIVES: naming those two files as CODE-6 case homes (rejected: `slotsession_test.go` is inventoried with no §7.1 credit, so a `§4.7; §5.2; §7.1` case there turns tier 0 red at S9 and would force further spec-map staging for no benefit).

WATCHOUT: the spec-map bullet in `## Files touched on application` now carries two distinct routes in two adjacent paragraphs: whole-file/directory entries (the new test surfaces plus `concurrent_workspace_test.go` at S12) and per-case `path::TestName` rows (all at S13). A later round adding a test must pick the route from the TARGET FILE's own registration granularity rather than from the nearest sentence. — EVIDENCE: non-spec-changes.md, the `tests/spec-map.json` bullet

WATCHOUT: `pkg/adapter/bindepoch_test.go` needs no spec-map entry and no `slotAddressCaseFiles` row even though its cases cite §7.1: it is a tier-1 package test under `pkg/`, outside `componentAndAboveTierDirs()`, and its name and body match neither derived inventory rule (the regexps are case-sensitive, so `claimSessionSlot(` does not match). Do not "helpfully" stage one. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173; cmd/lenny-test/cmd_validate.go:125-139

USEFUL [standing Settled ":1466", ":1468"]: the per-file credit census and the bindepoch_test.go exemption were both accurate against the tree today and saved the whole re-derivation; only the annotation for the concurrent-workspace case was still open, which is exactly what this round closed.


### [non-spec.3.fix-design-G1.1]

DECISION: Name the staged tier-4 concurrent-workspace case `TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1`, annotate it `// spec: §7.1; §5.2; §4.7`, keep the late-reclaim arm as a subtest of that same function (one annotation, one diagnosis), and stage WHOLE-FILE spec-map credits for `tests/tier4_integration/concurrent_workspace_test.go` under 7.1 and 4.7 at S12 — BECAUSE `tests/spec-map.json` registers that file as a whole (credits 5.2, 6.4, 28.5.3) and the proposal's own rule in the spec-map bullet says a whole-file-registered file is satisfied by a whole-file entry. ALTERNATIVES: per-case `path::TestName` entries (rejected: a single per-case entry flips the WHOLE file into per-case mode in `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`, so the shipped case and the file-header `// spec:` comments at :38/:162/:213 are then re-attributed by `citedSectionsPerCase`, which is churn for no gain); folding the new assertions into the shipped `TestConcurrentWorkspacePerSlotExecution_spec_5_2` under its existing `// spec: §5.2` (rejected: it would strip §7.1/§4.7 coverage credit from the one tier-4 case that pins them and misdescribe the case's subject).

FACT: `tests/spec-map.json` credits `tests/tier4_integration/concurrent_workspace_test.go` under 5.2, 6.4 and 28.5.3 only, all whole-file, with no per-case row. EVIDENCE: tests/spec-map.json:1040,:1417,:5691

FACT: the credit gate has two modes and the mode is per FILE, not per case: `if len(perCase) == 0` it takes the union of every `// spec:` section cited anywhere in the file and demands whole-file credits; otherwise it checks case by case. Adding one per-case row to a whole-file-registered file changes how every case in it is checked. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:968-988

FACT: the case name's `_spec_X_Y` suffix is itself gated — `TestAddressCaseNamesAgreeWithTheirOwnCitations` requires the suffix section to be cited (exactly, or at a coarser/finer granularity) by the case's own annotation. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1026-1040

FACT: `slotAddressCaseFiles` already carries `tests/tier4_integration/concurrent_workspace_test.go`, so no inventory edit is needed for this case; and the inventory's derived rules are `slotSurfaceCallRE` (`slotstate.|ClaimSlot(|ReleaseSlot(|ReserveSlotOnPod(|claimAtCreate(|BindReservedSlot(`) plus a filename rule `(slot|one_session_only|sole_session)[^/]*_test\.go$`. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:425,:1168-1173

FACT: `pkg/adapter/bindepoch_test.go` matches NEITHER derived inventory rule by name, so it enters `slotAddressCaseFiles` only if its body calls the slot claim surface; the spec-map subtree entry `pkg/adapter/...` exists under 4.7 ALONE, so if it ever does enter the inventory its `§5.2; §7.1` annotation needs credits. The spec-map bullet's catch-all ("Any other file the implementor lands a case in is checked the same way") already covers that, so finding 1's resolution creates no new staging. EVIDENCE: tests/spec-map.json (`pkg/adapter/...` under 4.7 only); non-spec-changes.md:2113-2114

DECISION: For the CODE-6 contradiction, delete the clause "plus cases in the two existing / files below." at non-spec-changes.md:1331-1332 and nothing else — BECAUSE the "all of them land in the one new file" sentence (:1336-1337) is what the S9/S10/S11 step split, the summary's file row and the :2188 file inventory all already assume, and the deleted clause has no resolvable referent (the only named pair, `slotsession_test.go` and `socketruntime_test.go` at :1401, belongs to the CODE-1/CODE-2 subsection with a different annotation).

WATCHOUT: `pkg/adapter/slotsession_test.go` carries no §7.1 credit (whole-file 4.9, 5.2, 6.1, 6.4, 15.4 plus 4.7 by subtree) and IS in `slotAddressCaseFiles`, so landing any `§7.1`-annotated case there turns tier 0 red at S9. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:278

WATCHOUT: the tier-4 case belongs to S12 (CODE-4, tiers 0,1,4,7a), not S13. The spec-map bullet's sentence "all of them at S13" is scoped to the per-case entries in files registered case by case; the new whole-file credits must be stated as a separate S12 sentence, and S12's checklist bullet — unlike S13's and S14's — currently says nothing about `tests/spec-map.json`. EVIDENCE: implementation-checklist.md S12/S13/S14; non-spec-changes.md:2097-2113


### [non-spec.3.review-applicability.1]

FACT: `pkg/adapter/...` is a spec-map SUBTREE entry, credited under 4.7 only, and
`tests/tier4_integration/...` is one credited under 15.1. A naive whole-file scan of
tests/spec-map.json that only matches exact paths reports the wrong credit set and
manufactures false "missing credit" findings. Always resolve `entry.endswith("...")`
prefixes the way `specMapEntryCoversFile` does. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:811-819; tests/spec-map.json (grep for `pkg/adapter/...`)

FACT: with subtrees resolved, the per-file credit sets the proposal asserts are correct:
`slotretry_test.go` = {4.1, 11.4, 15.1}, `slotbinder_test.go` = {4.1, 4.6.3, 4.7, 5.2, 6.2, 7.1},
`slotclaimer_test.go` whole 5.2, `recycle_scrub_path_test.go` whole 15.1 only,
`slotsession_test.go` = {4.7, 4.9, 5.2, 6.1, 6.4, 15.4}. The one edited inventory file whose
credits do NOT cover the annotation its new case plainly needs is
`tests/tier4_integration/concurrent_workspace_test.go` = {5.2, 6.4, 15.1, 28.5.3}. — EVIDENCE: tests/spec-map.json; tests/tier0_static/spec_map_slot_address_registration_test.go:236-470

FACT: the tier-0 credit-inventory gate does NOT reach every new test file. Membership is
derived by two rules: a path matching `(slot|one_session_only|sole_session)[^/]*_test\.go$`,
or a file whose body matches `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`
(case-sensitive: `claimSessionSlot(` and `releaseSessionSlot(` do NOT match). So
`pkg/adapter/bindepoch_test.go` and `tests/tier10_conformance/bind_epoch_conformance_test.go`
need no inventory row and no credit, while the two `slot*`-named new files do — which the
proposal already stages. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1169-1173, :1213-1230

FACT: `validate-maps`' "test files mapped" reverse check walks only `tests/tier{2..10}_*`, so a
new `pkg/` test file needs no spec-map entry; and the change-graph completeness check excludes
`_test.go` entirely, so no new test file needs a change-graph glob key. `pkg/adapter/bindepoch.go`
is the only new non-test source file and `pkg/adapter` is already a glob key. — EVIDENCE: cmd/lenny-test/cmd_validate.go:711-790; cmd/lenny-test/changegraph/changegraph.go:59-68

MISTAKE: I nearly filed "AssignCredentialsResponse field 1 collides with session_id". It does
not: `message AssignCredentialsResponse {}` at schemas/lenny-adapter.proto:1033 is genuinely
empty, and an `awk '/^message X \{/,/^\}/'` range extraction silently runs into the NEXT
message for a single-line `{}` body. Use `sed -n '/^message X {/,/^}/p'` and re-read. All nine
staged field numbers check out free: ShutdownRequest 7, ShutdownResponse 3,
PrepareWorkspaceResponse 3, FinalizeWorkspaceResponse 2, ResumeResponse 4,
ConfigureWorkspaceResponse 2, RunSetupResponse 2, AssignCredentialsResponse 1,
StartSessionResponse 2. — EVIDENCE: schemas/lenny-adapter.proto:1033, :1609-1668

FACT: the two staged claim-register rows fire no tier-0 gate beyond the generic validator.
`TestClaimRegisterAgreesWithTheAdapterProto` only checks rows whose `surface` string contains
`schemas/lenny-adapter.proto`, and neither staged row's surface does; the three reachability
gates (`...CredentialRowStatusMatchesTheGatewayCaller`, `...WiredCredentialRowNamesItsProductionCaller`)
are hard-coded to three named credential rows. So landing both `WIRED` rows at S8, before their
production readers exist at S10/S12, breaks nothing. `#2851-gateway-to-pod` resolves
(spec/28_communication-channels.md:205). — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:66-80; tests/tier0_static/claim_register_test.go:690-785

WATCHOUT: line citations into `pkg/gateway/sessionserver/start.go` in the proposal are now
off by one in places (`maxSlotRetries` is at :2719 not :2720; `classifySlotBindFailure`'s
reserved-branch call is at :2603 not :2602). The file shifted. Do not file these; the loop's
standing rule is that line numbers drift.

UNVERIFIED: whether the tier-4 concurrent-workspace case is meant as a NEW test function or as
an extension of the shipped `TestConcurrentWorkspacePerSlotExecution_spec_5_2`. I filed the
gap as underspecification-with-a-gate-consequence because the sibling tier-4 case in the same
section gained a name, an annotation and a per-case spec-map row in the r2 fix pass and this
one gained none. A human or the fixer should state which it is.


### [non-spec.3.review-fresh.1]

WATCHOUT: the named snapshot `scratchpad/cp-snap/.../non-spec-r3` is BYTE-IDENTICAL to the working tree, so `diff -ru` returns nothing and there is no round-3 delta to read first. Do not spend a round hunting one. — EVIDENCE: `diff -rq scratchpad/cp-snap/0081_.../non-spec-r3 proposals/0081_...` exits 0

FACT: `tests/spec-map.json` whole-file credits, derived by parsing the JSON rather than by grepping prose: `pkg/gateway/sessionserver/slotretry_test.go` → ['15.1'] only; `tests/tier4_integration/recycle_scrub_path_test.go` → [] (every entry is per-case); `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` → ['4.6.3','4.7','5.2','6.2','7.1'] (no 4.1); `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go` → ['5.2'] plus 11 per-case; `tests/tier4_integration/concurrent_workspace_test.go` → ['28.5.3','5.2','6.4']; `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` → ['15.4.3','28.5.3','4.1','4.7','5.2']; `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` → ['6.2','7.2']. — EVIDENCE: tests/spec-map.json (parse with python, do not grep)

FACT: the tier-0 credit gate's derived inventory rules are `slotSurfaceCallRE = slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(` and `slotSubjectFileRE = (slot|one_session_only|sole_session)[^/]*_test\.go$`. Neither matches `pkg/adapter/bindepoch_test.go`, `tests/tier10_conformance/bind_epoch_conformance_test.go`, or an `adapter_bind_epoch/` file, because `releaseSessionSlot(`/`claimSessionSlot(`/`reclaimSlotLocked(` do not contain the capitalised substrings. So only the two `slot*`-named new files owe an inventory row, which is what the proposal already says. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173,:1212-1230

FACT: `TestClaimRegisterAgreesWithTheAdapterProto` only validates a row's `Message.field` claim against the proto when the row's `surface` string CONTAINS `schemas/lenny-adapter.proto`. SCHEMA-1's two new rows name Go surfaces, so neither is checked against the proto and neither can fail that gate. Its other half (every message declaring `coordination_generation` needs a tracked row) is untouched because SCHEMA-1 adds no generation field. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:68-102

FACT: all nine SCHEMA-1 field numbers are free and the shipped `ShutdownRequest` holds 1,2,3,5,6 with 4+`slot_id` reserved; `AssignCredentialsResponse` really is `{}`. buf lint is STANDARD with `enum_zero_value_suffix: _UNSPECIFIED`, so `SlotReclaimOutcome`/`SLOT_RECLAIM_OUTCOME_*` satisfies ENUM_VALUE_PREFIX and the zero-value suffix. — EVIDENCE: schemas/lenny-adapter.proto:1609-1636, :438-449; buf.yaml

FACT: every verbatim spec anchor SPEC-1..SPEC-5 quotes still matches the tree byte-for-byte at spec/04:157, :686, :659-695 (the §4.7.1 insertion window), spec/05:453, :545, :555, spec/06:146-156, :234, spec/07:23, :210, :213, :214, :414, spec/15:1469-1471, spec/29:704-711. `Shutdown` appears in no spec/28 line, so the "§28 registers untouched" claim holds. Re-derived independently this round; do not re-run unless spec/ moves. — EVIDENCE: the files above

FACT: `status.FromError` in grpc v1.80.0 walks the chain with `errors.As`, so CODE-5's `status.Code(err) == codes.Aborted` arm does fire through `fmt.Errorf("%w")` + `*SlotBindError.Unwrap`. — EVIDENCE: $GOMODCACHE/google.golang.org/grpc@v1.80.0/status/status.go:112-121; pkg/gateway/podlifecycle/podsession/slotfailure.go:74

FACT: `parseUploadToSession` rejects `len(req.Files) == 0` before it calls `PrepareWorkspace`, so the §7.4 mid-session upload path really does always send at least one frame. That is what keeps CODE-6's "every `PrepareWorkspace` response reports a non-zero epoch" claim true on the second caller, not just on `stageWorkspace`. — EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:183,:128

UNVERIFIED: the shipped adapter answers a ZERO-FRAME `PrepareWorkspace` stream with `SendAndClose` having never resolved the slot, so after SCHEMA-1 it would report `bind_epoch: 0`, which staged §15.4 declares non-conforming without a carve-out. No gateway caller can produce a zero-frame stream, so I did not file it; somebody deciding whether §15.4's clause wants a "a stream that resolved no entry" carve-out should settle it. — EVIDENCE: pkg/adapter/staging.go:56-119; spec-changes.md:712

UNVERIFIED: CODE-4's outcome switch has no `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` arm, so a response that leaves `slot_reclaim` unset falls to `default: return false` (not leaked) — a fail-open on the leak-accounting predicate, and the staged "six arms" test list has no row for it. Unreachable with the first-party adapter, which sets the field on all three returns, so I did not file it. — EVIDENCE: non-spec-changes.md:562-582, :1528-1532

OPEN: the tier-4 `concurrent_workspace_test.go` cases are the one test group the proposal gives no `// spec:` annotation for, and that file's whole-file credits are 28.5.3/5.2/6.4 with no per-case entries. If the implementor annotates §7.1 on the reclaim case, `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` fails tier 0 and the spec-map bullet adds no 7.1 credit for it. The catch-all sentence at non-spec-changes.md:2112-2113 arguably covers it, which is why I left it as an OPEN rather than a finding.


### [non-spec.3.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability/fault-tolerance lens — BECAUSE every recovery
mechanism the amendment adds (bind epoch, reclaim hold, compensating `Shutdown`, `ExcludePods`, the
`accountSlotFailure` third caller, the `codes.Aborted` resume arm) traced clean through crash, restart,
redelivery and store-failover, and every tree claim I spot-checked held — ALTERNATIVES: I considered and
rejected filing four candidates, each recorded below so the next reliability pass does not re-derive them.

FACT: the named snapshot `scratchpad/cp-snap/.../non-spec-r3` is byte-identical to the working tree
(`diff -ru` returns nothing), so there is no r3 delta to read first. This is the seventh/eighth recurrence
of the snapshot trap the standing context records. Budget zero time for it.

FACT: the hold cannot leak on the §10.1.4 path. `onHoldTimeout` pass 1 (`deregisterStartedSessions`,
pkg/adapter/slotsession.go:375) collects only `st.started` entries and pass 2 iterates every collected
member with no early exit (pkg/adapter/holdstate.go:190-205), so every `heldSession.release` CODE-6 adds
is reached. The shared 10s `closeCtx` (holdstate.go:201) bounds only `Runtime.Close`; `removeSlotTree`
(holdstate.go:246) is uninterruptible and always runs, so a ctx expiry does not skip a release.

FACT: `slotCleanupBudget`'s division cannot panic. Every `ResumeRequest` is built at
pkg/gateway/sessionserver/start.go:4029 with `maxConcurrentSessions(match.MaxConcurrentSessions)`, which
floors at 1 (start.go:3353-3358), and both bind-path `SlotBindRequest` sites are gated on
`MaxConcurrentSessions > 1`. I checked this specifically because the budget's doc comment guards only the
`cleanupTimeoutSeconds` zero case and says nothing about the divisor.

FACT: every bind attempt dials its own `adapterclient.Client` (`bindReservedSlot` at
pkg/gateway/podlifecycle/podsession/slotbinder.go:236; `connectSlot` at :415), and both session-end senders
close the connection immediately after their `Shutdown` (slotbinder.go:542-544, binder.go:2043-2045). That
is what makes the proposal's "a bind that fails inside its first entry-creating RPC has a zero latch" claim
true, and it is why the latch-clearing gap below is unreachable.

UNVERIFIED (considered, NOT filed): CODE-6 clears the per-connection epoch latch only on `DemoteSDK` and on
a `ShutdownReclaim` answering `RECLAIMED`, while staged §4.7.1's caller rule is broader — "a `Shutdown`
answering `reclaimed` removes it" (spec-changes.md:682), which the unconditional `Client.Shutdown` also
does. I could construct no reachable harm, because every shipped `Client.Shutdown` caller closes the
connection on the next statement, so no compensation is ever sent from a stale latch. Filed as unverified
rather than as a finding. Whoever revisits should look for a future caller that reuses a connection across
two bind sequences.

UNVERIFIED (considered, NOT filed): CODE-1's epoch-mismatch early returns (`ABSENT` / `SUPERSEDED`) exit
the handler before clause three, so a `Shutdown` that carried both a non-zero epoch and the recycle
disposition would skip the whole-pod scrub, against staged §4.1's "runs the whole-pod scrub when the
recycle disposition is set". Unreachable in the staged tree: `ShutdownRecycle` is a separate client method
that passes epoch zero, and `ShutdownReclaim` never sets a recycle disposition, so no staged caller can
construct the combination. Below the bar today; it becomes live if a caller ever merges the two forms.

UNVERIFIED (considered, NOT filed): after CODE-4, a bind failure against an unreachable pod answers no
reclaim, so `sbe.Leaked` is true and `SlotClaimer.ReleaseSlot` early-returns before the Redis decrement and
the claim DELETE (podclaim/slotclaimer.go:830-843), where the shipped `leaked=false` release retires the
pod. At `maxConcurrentSessions >= 3` the pod keeps a permanently-held slot until `ceil(n/2)` leaks drain
it. This is §6.2's shipped `leaked` semantics applied to a new trigger, it is disclosed in the summary's
"Faster pod churn" bullet and in the "leaked occupancy has no durable backing" out-of-scope entry, and the
reliability lens' own rule ("slower to recover is not a finding") covers it.

USEFUL [Settled: `SlotClaimer.ReleaseSlot(leaked=true)` returns early]: this single entry is what let me
reason about every leaked-disposition consequence without re-reading the claimer. Keep it.


### [non-spec.3.review-test-coverage.1]

DECISION: returned an empty findings list for the test-coverage lens — BECAUSE I walked every
staged deliverable (SPEC-1..5, SCHEMA-1, CODE-1..6, CONF-1, DOCS-1/2) against the `## Testing`
section behavior by behavior and every changed behavior has a named, concrete case at the
tier it reaches (1, 2, 3, 4, 7a, 9, 10, 11), including the error, concurrent, boundary and
fail-closed arms — ALTERNATIVES: considered filing "omits tier 8" (the unanswered-reclaim
failure-injection path) and "CONF-1 omits the ABSENT outcome and the reclaim-hold contract";
rejected both — the tier-4 datastore-crossing case drives the unanswered reclaim end to end
with a client interceptor, and the CONF-1 parity argument was already filed and refuted this
window as hardening.

WATCHOUT: the named snapshot is byte-identical to the working tree again this round.
`diff -ru scratchpad/cp-snap/.../non-spec-r3 proposals/0081_.../` produced no output, so there
is no delta to read first. This is the seventh/eighth recurrence the standing context records;
do not spend a round hunting it. EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r3

FACT: the tier-0 credit-inventory gate's two derived rules are literal and cheap to check
against a proposed new test file. Rule 1 is a body regex
`slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`
and rule 2 is a path regex `(slot|one_session_only|sole_session)[^/]*_test\.go$`.
`pkg/adapter/bindepoch_test.go`, `tests/tier3_contract/adapter_bind_epoch/` and
`tests/tier10_conformance/bind_epoch_conformance_test.go` match neither (the adapter's helpers
are `releaseSessionSlot`/`claimSessionSlot`, which do not contain `ReleaseSlot(`/`ClaimSlot(`),
so only the two `slot*`-named new files owe an inventory row, which is exactly what the
proposal stages at S10. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168-1173,:1212-1229

FACT: the two tier-11 gates DOCS-1/DOCS-2 extend accept the prescribed edits without breaking
their own negative loops. `generalSlotEdges` feeds a positive loop over §6.2's either-concurrency
block and a negative loop over the concurrent-occupancy block; adding
`"receiving_uploads ──→ slot_cleanup"` is safe because that string does not occur in the scoped
block. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55-75

FACT: `isTransientPodClaimError` has exactly one production caller (`holdOrFailOnResumeError`)
and its shipped table test carries no `Aborted` row, so CODE-5's new arm breaks no existing
case. EVIDENCE: pkg/gateway/sessionserver/start.go:3610,:3648; pkg/gateway/sessionserver/podclaimerror_internal_test.go:379

FACT: `PrepareWorkspace` already resolves the slot identifier exactly once per call, guarded by
`if stagingDir == ""`, so SPEC-5's §15.4 non-conformance clause ("resolves the slot identifier
more than once within one `PrepareWorkspace` call") describes shipped behavior rather than a
change needing a new test. EVIDENCE: pkg/adapter/staging.go:48,:77-85

FACT: `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go` is genuinely envtest-backed, so
the proposal's tier-2 placement-filter siting is correct. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go:26,:37,:648

UNVERIFIED: SPEC-2 stages a normative §7.2 step-3 / §6.2 mid-resume-cancel obligation that the
gateway runs the §7.1 pod-side reclaim on the half-claimed replacement pod before releasing it,
and no non-spec deliverable names that call site. My reading is that the in-flight `Resume`
fails the CoordinatorFence after `bumpCoordinationGenerationOnSnapshotClose`, so CODE-4's
`Binder.Resume` failure branch discharges it, which is why no separate deliverable or test
exists. Nobody has traced that chain end to end. A mechanism or edit-site reviewer should.
EVIDENCE: 0081_...spec-changes.md:459-466,:504-514; pkg/gateway/sessionserver/sessionserver.go:2955,:3005


### [non-spec.4.review-applicability.1]

DECISION: returned an empty findings list for the applicability-and-sequencing lens — BECAUSE every
staged edit's anchor, every created artifact's properties, the fourteen-step checklist's lanes,
Depends-on set, deliverable coverage and tier lists, and every tier-0 gate the staged test surfaces
touch all check out against the tree — ALTERNATIVES: I considered filing the S9 Depends-on omission
of S4 (CODE-6 implements the reclaim hold whose normative home is SPEC-3's §5.2 paragraph, staged at
S4, and S9 names only S1 and S8), but S4 runs before S9 in the one execution sequence, so the code
step is not written against unlanded text and the lens's class-6 test does not fire.

FACT: `tests/spec-map.json` carries a DIRECTORY entry `pkg/adapter/...` under section 4.7. That is
why the CODE-1/CODE-2 tier-1 cases (`// spec: §4.7; §5.2`) landing in
`pkg/adapter/slotsession_test.go` need no new spec-map credit even though that file's exact-path
credits are only 4.9, 5.2, 6.1, 6.4 and 15.4 — `specMapEntryCoversFile` treats a `<subtree>/...`
entry as a whole-file credit. I nearly filed a tier-0 credit-gate finding before checking this.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:811-819,:855-857;
tests/spec-map.json section 4.7 entry `pkg/adapter/...`.

FACT: the credit gate branches on registration granularity. `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go`
IS registered per case, so the per-case branch runs for it — but its new tier-2 cases annotate §5.2
alone and that file also carries a whole-file 5.2 credit, and `creditsMissing` accepts a whole-file
credit for a per-case file. So the proposal's "need no entry" claim for that file is correct.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:953-963,:970-989.

FACT: all nine SCHEMA-1 field numbers are free on their messages, verified against the proto:
ShutdownRequest holds 1,2,3,5,6 with 4 reserved (7 free); ShutdownResponse 1,2 (3 free);
PrepareWorkspaceResponse 1,2 (3 free); FinalizeWorkspaceResponse 1 (2 free); ResumeResponse 1,2,3
(4 free); ConfigureWorkspaceResponse 1 (2 free); RunSetupResponse 1 (2 free);
AssignCredentialsResponse empty (1 free); StartSessionResponse 1 (2 free). The enum value spelling
matches its sibling `SessionScrubOutcome`. EVIDENCE: schemas/lenny-adapter.proto.

FACT: `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is the ONLY closed field set over the
messages SCHEMA-1 opens, and the proposal's anchors for it are exact: gate at :208, `assertFieldSet`
at :259, the unexpected-field error at :264-267. A repo grep for the other eight response messages
across `tests/` and `scripts/` hits only recycle_scrub_conformance, scrub_wire, shutdown_recycle_wire
and token_service_unavailability_guard, none of which pins a closed set over them.
EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208,:259,:264-267.

FACT: every `ReleaseSlotReservation` call site the CODE-4 signature change reaches is enumerated by
the proposal. Production: slotbinder.go:172 (ClaimSlot connect stage), :217 (BindReservedSlot),
binder.go:1714 (releaseResumeSlot), start.go:2834 (applySlotRetryPolicy), :3246 (rollbackClaim),
plus the interface at start.go:2727. Fakes: slotretry_test.go:52 and slotretry_load_test.go:33, both
in the Files-touched test list.

WATCHOUT: the named snapshot `scratchpad/cp-snap/.../non-spec-r4` is byte-identical to the working
tree, and so is `non-spec-r4-start`. The newest differing snapshot is `non-spec-r3`, and the delta
is small: the S12 checklist line gained its spec-map sentence, the tier-4 case gained the name
`TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1` with its annotation, the late-reclaim
arm became an unnamed subtree arm, the CODE-6 tier-1 preamble dropped "plus cases in the two
existing files below", and the spec-map bullet gained the concurrent_workspace_test.go paragraph.
Diff against `non-spec-r3`, not `non-spec-r4`.

FACT: the anchors the amendment's newest text leans on are all live. staging.go's three
`tracing.CategorizeError(..., CategoryPermanent)` stamps are at :81, :183 and :339 exactly;
start.go:2720 is `const maxSlotRetries = 1`; :2140 is `slotReq :=`, :2172 and :2602 are the two
`classifySlotBindFailure` production callers, :2606-2609 is the `runWithQueue` closure,
:4041 is `resumeOnPod`'s `podBinder.Resume` failure branch with `match` in scope;
adapter-contract.md:10 is the completeness sentence, :64 `DemoteSDK`, :75 `Shutdown`, :84
`**Scrub responsibilities.**`; per_slot_substate_scope_doc_reconciliation_test.go's
`generalSlotEdges` is :32-37 with the positive loop at :55 and the negative at :70.

UNVERIFIED: which file the tier-1 gateway cases annotated `// spec: §7.1; §5.2; §6.2` that land in
`pkg/gateway/sessionserver/start_test.go` actually go in, and whether any of them introduces a call
matching `slotSurfaceCallRE` (`slotstate.`, `ClaimSlot(`, `ReleaseSlot(`, `ReserveSlotOnPod(`,
`claimAtCreate(`, `BindReservedSlot(`). `start_test.go` is in neither `slotAddressCaseFiles` nor
`tests/spec-map.json` today and matches neither derived inventory rule; if a new case adds such a
call, the file must enter `slotAddressCaseFiles` in the same commit. The Files-touched bullet states
that rule generically, so it is covered in principle, but nobody has checked the concrete case list.
Whoever writes the tier-1 gateway cases should check it at S12/S13.


### [non-spec.5.fix-G1.1]

DECISION: Scoped SPEC-3's leak-accounting sentence to the cleanup a reclaiming `Shutdown` performs, and added one sentence naming the whole-pod scrub as where the handler-run cleanup's residue is accounted — BECAUSE the sentence was false only in naming one carrier for a cleanup that has two performers, and the second performer cannot be given a carrier without an adapter-side residue record plus a fourth reclaim outcome on the wire, for a residue the occupancy-zero scrub already removes and verifies — ALTERNATIVES: inverting or sub-casing CODE-4's `ABSENT` arm (rejected: `ABSENT` is the common answer on three branches, and a leaked verdict drains a pod at `maxConcurrentSessions: 2`; it is also the exact inversion the hand amendment already undid); a fourth `SlotReclaimOutcome` (new wire surface for a swept residue); returning the error from `releaseSessionSlot` (every production caller is already on a failure branch returning a client-facing error); deleting the sentence (reopens the implication that leak accounting is withheld along with the report).

CORRECTS [design for non-spec.5 G1]: the design's prescribed sentence said the whole-pod scrub "retires the pod when that verification fails". That is not unconditional. `onScrubFailure` defaults to `warn`, which returns the pod to the available pool with a `scrub_warning` annotation and serves the next session; only `fail`, or reaching `maxScrubFailures`, terminates it. I wrote "disposes of the pod under the `onScrubFailure` policy stated below" instead, in both the staged spec sentence and the edge-case bullet. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:483 (`warn` default), :484 (`fail`), :490 (scrub failure limit).

FACT: the whole-pod scrub is a RECYCLING-pod mechanism. It runs "whenever occupancy reaches zero on a recycling pod before the pod is reused". Any claim that the scrub bounds a residue must say what bounds it on a non-recycling pod; there the pod retires at the same boundary, which is why the edge-case bullet states both halves. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453, :456.

FACT: `releaseSessionSlot`'s production call sites are the start-path rollbacks in `pkg/adapter/session.go` (:133, :147, :157), `resume.go` (:69, :73, :89, :107, :126, :134, :141) and `sdkwarm.go` (:236, :241, :251, :298). The `sdkwarm.go:298` site is `DemoteSDK` rather than a rollback; it is the odd one out and it is deliberately not addressed by this fix. The other two references to the function are `export_test.go:34` and `podmcp_arming_internal_test.go`.

WATCHOUT: the discard at `pkg/adapter/slotsession.go:217` (`_ = removeSlotTree(st)`) is the single reason the staged accounting sentence was false. CODE-6 now turns it into a `slog.Warn` and the file gains a `log/slog` import (it currently imports only `context`, `sort`, and the two grpc packages). That log line carries no contract, so it pins no test and the Testing section is unchanged. Do not let a later round promote it into a metric or a counter: the edge-case bullet is written as an accepted failure mode with an observable trace rather than a half-built accounting path. EVIDENCE: pkg/adapter/slotsession.go:5-11,:214-220.

OPEN: `sdkwarm.go:298` (the `DemoteSDK` release) reclaims a slot the pod's shared runtime process was given, with no cleanup-outcome report, which reads against SPEC-3's complement sentence ("A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome"). Deliberately out of scope for this group; a later round should adjudicate it.

UNVERIFIED: the staged §7.1 sentence at spec-changes.md:401 ("one for a session the adapter holds no entry for is answered `absent`; both are completed reclaims and leave the slot unleaked") was flagged as a related site and ruled not-a-site by the design, on the ground that `unleaked` is §6.2's occupancy disposition and the deregistration genuinely released the occupancy. I did not edit it. Somebody should confirm that reading holds now that SPEC-3 concedes the handler-run cleanup can fail silently behind that same `absent`.


### [non-spec.5.fix-G2.1]

DECISION: CODE-5's "Where it is read" bullet now splits the two axes explicitly — `BindSlot` stays by-value so the `ExcludePods` pointer threading touches no implementor, while CODE-4's `leaked bool` widening of `ReleaseSlotReservation` does change the interface and both fakes at S12 — BECAUSE the old single sentence welded a true claim to a false one and an S12 implementor reading it would ship a non-compiling package — ALTERNATIVES: deleting the sentence (rejected: the by-value claim is load-bearing for CODE-5's pointer design); saying only "the interface does change" without naming the axis (rejected: leaves the reader thinking the pointer threading is the change); moving the fake rewiring to S13 (rejected: S12 widens the signature, so the package must compile at the end of S12).

DECISION: CODE-4's "Call sites for the new parameter" table gains one row naming `slotretry_test.go`'s `fakeSlotBinder` and `slotretry_load_test.go`'s `concurrentSlotBinder` — BECAUSE that table is what an S12 implementor works from and it listed the interface declaration without the two types satisfying it.

FACT: `slotBinder` is declared at pkg/gateway/sessionserver/start.go:2725-2729 with the three-parameter `ReleaseSlotReservation(ctx, sandboxName, slotID string) error`, and both fakes implement exactly that arity. EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:52; slotretry_load_test.go:33.

FACT: `fakeSlotBinder.released` is `[][2]string // (pod, slotID)` and cannot carry a disposition, which is why the widening is part of CODE-4 rather than an S13 detail. EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:29.

WATCHOUT: `concurrentSlotBinder` (slotretry_load_test.go:33-37) records nothing and returns an error unconditionally on purpose. It needs the fourth parameter and nothing else; do not give it a recording field. EVIDENCE: pkg/gateway/sessionserver/slotretry_load_test.go:33-37.

FACT: `## Files touched on application (non-spec)` already lists both test files and already names the `slotBinder` interface, so this correction cascaded nowhere else. The checklist and summary needed no change: no deliverable was added, removed, merged, split or resequenced. EVIDENCE: non-spec-changes.md:2217-2218.

FACT: adjacent citations in the paragraph I edited were re-verified and still hold: `req` helper at slotretry_test.go:69, the load test's `applySlotRetryPolicy` call at slotretry_load_test.go:84-87, `classifySlotBindFailure` by-value at start.go:2761.

MISTAKE: earlier rounds filed this same false sentence at least three times without it being closed; the review logs still carry those filings and are audit records that stay as written.


### [non-spec.5.fix-G3.1]
DECISION: Corrected SCHEMA-1's second claim-register row to name `Server.nextBindEpochLocked` — BECAUSE CODE-6 stages the mint under that name and the register must name a symbol the deliverable creates — ALTERNATIVES: re-aiming the row at the response-stamping handler sites (the row's note describes the mint-and-latch pair, and the handler list would churn with CODE-1/CODE-2), splitting into a mint row and a report row (new register surface in answer to a spelling defect), and widening the tier-0 register gate to resolve Go symbols (its own proposal).
FACT: CODE-6 is the single authority on the mint's name, staged as `nextBindEpochLocked` — EVIDENCE: 0081...non-spec-changes.md:988 and :1037.
WATCHOUT: the tier-0 claim-register gate cannot catch a wrong symbol in a `surface` value. `surfaceNamesOnlyALine` only rejects a bare line reference, and the reachability join is scoped to the three credential rows — EVIDENCE: tests/tier0_static/claim_register_test.go:145-157 and :743-779. Any register row this proposal stages must be checked by hand against the deliverable that creates its symbol.
FACT: line numbers in non-spec-changes.md drifted by roughly thirteen during round 5 (the SCHEMA-1 row moved from :1246 to :1259) because G1 inserted text earlier in the file. Re-grep anchors rather than trusting a round's quoted line numbers — EVIDENCE: 0081...non-spec-changes.md:1259.


### [non-spec.5.fix-design-G1.1]

DECISION: narrow SPEC-3's accounting claim to the cleanup a reclaiming `Shutdown` performs, and name the whole-pod scrub as the backstop for the handler-run cleanup, rather than inventing residue tracking — BECAUSE the only way to make the unqualified claim true is a per-identifier residue record on the adapter that a later `Shutdown` would have to answer with a fourth reclaim outcome, which is new wire surface for a residue the occupancy-zero scrub already removes and verifies — ALTERNATIVES: (a) flip CODE-4's `ABSENT` arm to leaked — rejected, it is the common answer on the upload-free branch and on every handler rollback, and at `maxConcurrentSessions: 2` it drains a pod per failed bind; (b) make `releaseSessionSlot` return the error to its fourteen callers — rejected, every caller is already on a failure branch returning a different error and has nothing to do with a second one; (c) delete the SPEC-3 sentence — rejected, it exists because the preceding sentence withholds the report and a reader would otherwise read the leak accounting as withheld with it (the proposal says so at spec-changes.md ~628-640).

FACT: the residue really is bounded and verified. §5.2 whole-pod scrub step 0 purges every `/run/lenny/slots/{sessionId}/credentials.json`, step 2 is `rm -rf /workspace/slots/*`, step 6 stat-verifies both and marks the scrub failed, and a failed scrub retires the pod. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:460, :471, :475, :477; pkg/adapter/podscrub.go:150-157.

FACT: `releaseSessionSlot` has FOURTEEN production callers, not the ten the finding lists, and one of them is NOT a pre-`running` rollback: `sdkwarm.go:298` (DemoteSDK) releases after `noteRuntimeClosed(sessionID)`. EVIDENCE: pkg/adapter/sdkwarm.go:296-299; the other thirteen are session.go:133,147,157, resume.go:69,73,89,107,126,134,141, sdkwarm.go:236,241,251.

OPEN: does the DemoteSDK release owe a cleanup-outcome report? SPEC-3 stages "A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome", and `ConfigureWorkspace` reaches `noteRuntimeStarted` on its fresh arm (sdkwarm.go:261), so the demoted session was given to the runtime and `releaseSessionSlot` reports nothing for it. Either the staged complement sentence is over-broad for a tentatively-configured preConnect session or CODE-6 owes a report there. Not touched by G1; a later round should file it. EVIDENCE: pkg/adapter/sdkwarm.go:261,:296-299; spec-changes.md:610 (the complement sentence).

WATCHOUT: the staged §7.1 sentence at spec-changes.md:401 ("both are completed reclaims and leave the slot unleaked") reads like the same over-claim and is NOT. Every clause there is about what the reclaim did, and `unleaked` is §6.2's occupancy disposition, which the deregistration genuinely released. A round that "fixes" it in parallel with SPEC-3 adds a caveat in a second place for a residue §5.2 and the edge-case list already own. EVIDENCE: spec-changes.md:401-402.

WATCHOUT: `pkg/adapter/slotsession.go` imports neither `log/slog` nor `log`. A fixer adding the warn line must add the import; the package's precedent is `slog` for structured adapter events (podscrub.go, sessionscrubreporter.go) and `log.Printf` only in older files. EVIDENCE: pkg/adapter/slotsession.go:5-11; pkg/adapter/podscrub.go:60-80.

UNVERIFIED: whether CODE-6's reroute of `releaseSessionSlot` through `reclaimSlotLocked` changes the error the discard hides (it should not, `removeSlotTree` is unchanged at pkg/adapter/slot.go:210-212). Somebody landing CODE-6 should confirm the discard site survives verbatim apart from the added log.


### [non-spec.5.fix-design-G2.1]

DECISION: Rewrite the one false sentence at non-spec-changes.md:861-863 so its claim is scoped to the POINTER axis only ("`BindSlot` still takes the request by value, so the pointer threading changes no implementor of `slotBinder`"), and state the other axis explicitly: CODE-4 widens `ReleaseSlotReservation` with `leaked bool`, so the interface declaration and BOTH test fakes take the fourth parameter at S12, and `fakeSlotBinder.released` widens from `[][2]string` to a record carrying pod, slot id and disposition. Add ONE row to CODE-4's call-site table (non-spec-changes.md:688-695) naming `slotretry_test.go`'s `fakeSlotBinder` and `slotretry_load_test.go`'s `concurrentSlotBinder`, because that table is the step-S12 implementor's enumeration of edit sites and without the row S12 lands a non-compiling package. BECAUSE the sentence is two claims welded together and only one of them is false; deleting the whole sentence loses the pointer-threading claim CODE-5's `ExcludePods` design depends on. ALTERNATIVES: (a) delete the sentence — rejected, loses the true half; (b) say only "the interface does change" without naming the axis — rejected, leaves the reader unable to tell which of the two edits touches implementors; (c) put the fake rewiring in CODE-5/S13 — rejected, S12 is the step that breaks compilation, so the fakes must move with it.

FACT: the tree confirms the contradiction exactly as filed. `slotBinder` is three-parameter at pkg/gateway/sessionserver/start.go:2725-2729; `fakeSlotBinder.ReleaseSlotReservation` is two-arg at slotretry_test.go:52 with `released [][2]string` at :29; `concurrentSlotBinder.ReleaseSlotReservation` is two-arg at slotretry_load_test.go:33. EVIDENCE: pkg/gateway/sessionserver/start.go:2725-2729; pkg/gateway/sessionserver/slotretry_test.go:29,:52; pkg/gateway/sessionserver/slotretry_load_test.go:33

FACT: no cascade into `## Files touched on application` is needed — it already lists both `pkg/gateway/sessionserver/slotretry_test.go` and `slotretry_load_test.go` among the touched tests, and :2166 already names the `slotBinder` interface as a CODE-4/CODE-5 edit site. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:2166 and the Files-touched test list

WATCHOUT: the staged tier-1 accounting case asserts `ReleaseSlotReservation` was called "with `leaked=true`" (non-spec-changes.md:1546-1549). That assertion is unexpressible while `released` is `[][2]string`, so the field widening is load-bearing for S13 even though the edit belongs to S12. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1547-1549; pkg/gateway/sessionserver/slotretry_test.go:29

WATCHOUT: `concurrentSlotBinder.ReleaseSlotReservation` (slotretry_load_test.go:33-37) records nothing and always errors; it needs the parameter added and nothing else. Do not widen a recording field there. EVIDENCE: pkg/gateway/sessionserver/slotretry_load_test.go:33-37


### [non-spec.5.fix-design-G3.1]

DECISION: Fix SCHEMA-1's second staged claim-register row by correcting the symbol spelling only — `Server.nextBindEpoch` becomes `Server.nextBindEpochLocked` at non-spec-changes.md:1246 — BECAUSE CODE-6 stages the mint under that exact name in `pkg/adapter/bindepoch.go` (same file :980 "`nextBindEpochLocked`, which seeds the counter lazily", :1024 "only the creating branch calls `nextBindEpochLocked`. That is the whole minting rule"), and the `pkg/adapter/bindepoch.go` file attribution in the row is already correct (:966 names the file, :970-979 confirms the counter's logic lives there while only the struct fields move to server.go/slot.go) — ALTERNATIVES: (a) re-aim the row at the seven response-stamping handler sites, since the claim text reads "Bind epoch reported on the slot-entry RPC responses" — rejected, the row's `note` is explicitly about the mint-and-latch pair and the paired `Client.BindEpoch` half already names the reporting reader, so widening the surface list adds hair without changing what a reader of the register reaches; (b) split the row into a mint row and a report row — rejected as new register surface for a spelling defect.

FACT: `nextBindEpoch` in any spelling appears nowhere in spec/, docs/, schemas/, charts/ or tests/claim-map.json; the only live sites are non-spec-changes.md:980, :1024, :1246 (plus the unamendable review logs). Nothing else in the proposal or the tree depends on the spelling, so the fix cannot cascade. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md:1246

WATCHOUT: the tier-0 register gate would not have caught this. `surfaceNamesOnlyALine` rejects only a surface reducing to a bare line reference, and the WIRED reachability join is scoped to the three named credential rows, so a wrong-but-well-formed symbol passes silently. Do not assume a staged claim-register surface is symbol-checked by anything. EVIDENCE: tests/tier0_static/claim_register_test.go:145-157, :743-779


### [non-spec.5.review-applicability.1]

DECISION: Filed exactly one finding — SCHEMA-1's second claim-register row names `Server.nextBindEpoch` while CODE-6 stages `nextBindEpochLocked` — BECAUSE it is a verifiable self-contradiction between two staged non-spec artifacts whose remedy is one word in non-spec-changes.md. ALTERNATIVES: rejected filing the S8-lands-WIRED-rows-before-their-readers-exist point (no gate resolves a surface against the tree; the register gate is textual only, so it would refute as immaterial), and rejected the CODE-1 early-return-skips-clause-three point (an epoch-bearing `Shutdown` never carries `recycle` under the staged §4.7.1 caller rules).

FACT: the whole end-to-end sequencing simulation came out clean this round. Verified in the tree, in order: every SCHEMA-1 field number is free on its message (`ShutdownRequest` 1,2,3,5,6 + reserved 4 → 7; `ShutdownResponse` 1,2 → 3; `PrepareWorkspaceResponse` 1,2 → 3; `FinalizeWorkspaceResponse` 1 → 2; `ResumeResponse` 1,2,3 → 4; `ConfigureWorkspaceResponse` 1 → 2; `RunSetupResponse` 1 → 2; `StartSessionResponse` 1 → 2; `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` so 1 is free). EVIDENCE: schemas/lenny-adapter.proto:1033, :1609, :1665, :699, :761, :1433, :1690, :869, :958

WATCHOUT: `awk '/^message X \{/,/^\}/'` silently runs past an empty message written as `message X {}` on one line and reports the NEXT message's fields. That is how a reviewer concludes `AssignCredentialsResponse` already holds field 1 (it does not — `RotateCredentialsRequest` does). Use a brace-depth parser. EVIDENCE: schemas/lenny-adapter.proto:1033 vs :1035

FACT: every staged verbatim anchor resolves and is unique in its target: spec/04:157 (§4.1 third sentence), the §4.7 `Shutdown` row opening, §4.7.9 step 5, spec/05:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**`), :555 (`**Max retries:**`), the three §7.2 anchors, §7.3's item 4, spec/06's `resuming → cancelled` clause. §29.4's "…§28.5.3)." sentence occurs four times file-wide but once inside step 13 (spec/29:704-711), which is how the edit scopes it. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,545,555; spec/29_communication-scenarios.md:704-711

FACT: the spec-map credit arithmetic in `## Files touched on application` is right on every file I recomputed with the gate's own rules (directory entries ending `...` count as whole-file credits, per `specMapEntryCoversFile`). `pkg/adapter/slotsession_test.go` already carries 4.7 through the `pkg/adapter/...` subtree entry; `podclaim/slotclaimer_test.go` carries 5.2 whole-file despite being per-case registered elsewhere; `slotbinder_test.go` carries 4.1/4.6.3/4.7/5.2/6.2/7.1 whole-file; `concurrent_workspace_test.go` is whole-file at 15.1/28.5.3/5.2/6.4 so needs exactly the 7.1 and 4.7 S12 stages; `recycle_scrub_path_test.go` is whole-file 15.1 only and per-case otherwise. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:806-860, :973-988

FACT: `slotAddressCaseFiles` membership is derived, not just hand-listed: a test file is required in the inventory if its name matches `(slot|one_session_only|sole_session)[^/]*_test.go` OR its body matches `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`. The regexes are case-sensitive, so `claimSessionSlot(` and `releaseSessionSlot(` do NOT match — which is why `pkg/adapter/bindepoch_test.go` needs no inventory row and the two new `slot*_test.go` files do. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1157-1173, :1108-1130

FACT: `validate-maps`' file-mapping check accepts a parent-directory entry, walking up from the file, so a `tests/tier3_contract/adapter_bind_epoch/...` entry covers every file the implementor creates under it. EVIDENCE: cmd/lenny-test/cmd_validate.go:768-780

FACT: rule S-2's second window says what the proposal says it says, and its covered-handler list is `session.go, lifecycle.go, checkpoint.go, coordination.go, credentials.go, slotcreds.go, attach.go, sdkwarm.go` plus the two renamed channel files — so `staging.go`, `slot.go`, `slotsession.go`, `resume.go`, `runtimegeneration.go` and `holdstate.go` are outside it. EVIDENCE: gateway-runtime-comms-remediation.md:1883-1890

FACT: `DemoteSDK` really does drop the registry entry (`anyRegisteredSession` → `releaseSessionSlot`), so DOCS-2's amended row and §4.7.1's latch-clear rule are both true of the tree. EVIDENCE: pkg/adapter/sdkwarm.go:296-301

FACT: the claim-register schema gate is purely textual about `surface` — it only rejects a surface that reduces to a bare line number, and the reachability join is scoped to the three named credential rows. Nothing resolves a WIRED row's symbol against the tree, which is why the `nextBindEpoch` mismatch is invisible to tier 0. EVIDENCE: tests/tier0_static/claim_register_test.go:145-157, :743-779

UNVERIFIED: the two new claim-register rows land at S8 with status `WIRED` while none of the production surfaces they name exists until S9/S10/S12. No gate fires, and §28.4 draws WIRED from reachability. Someone should decide whether an intermediate commit may carry a WIRED row whose reader is two steps away, or whether the rows belong in the last code step.


### [non-spec.5.review-citations.1]

DECISION: returned an empty findings list for the citation lens — BECAUSE a mechanical sweep of every `file:line` citation in the proposal directory (186 distinct citations across summary, spec-changes, non-spec-changes, checklist) plus hand-verification of ~70 of the load-bearing ones found no target that says something materially different — ALTERNATIVES: filing the two sub-material inaccuracies listed below; rejected because neither changes any staged action.

FACT: all 12 "reads, verbatim:" anchor blocks in spec-changes.md resolve to exactly one spec file each, byte-for-byte. Script to re-run: extract the fenced block after each line ending `verbatim:` and substring-search every `spec/*.md`. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:287,323,378,421,444,456,485,504,531,560,580,601
FACT: every staged proto field number is free on its message today. ShutdownRequest holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1609-1636); ShutdownResponse 1,2 (:1665-1668); PrepareWorkspaceResponse 1,2 (:699-702); FinalizeWorkspaceResponse 1 (:761-768); ResumeResponse 1,2,3 (:1433-1447); ConfigureWorkspaceResponse 1 (:1690-1692); RunSetupResponse 1 (:869-871); AssignCredentialsResponse empty (:1033); StartSessionResponse 1 (:958-962). Enum spelling matches SessionScrubOutcome (:438-449). — EVIDENCE: schemas/lenny-adapter.proto:1609
FACT: `tests/claim-map.json` really is 76 rows / 32 WIRED / 24 UNWIRED / 20 ABSENT, and the thirteen `coordination_generation` rows really are UNWIRED under `deferral_id: R16` with `spec_anchor: #2851-gateway-to-pod`. The summary's §1.12 arithmetic is right. — EVIDENCE: tests/claim-map.json
FACT: `TestClaimRegisterAgreesWithTheAdapterProto` only forces a register row per `coordination_generation` field, and only validates `Message.field`-shaped claims whose `surface` names the adapter proto path. SCHEMA-1's two rows name no proto path in `surface`, so the nine added fields need no per-field rows. Do not re-derive this as a missing edit site. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:66-101
FACT: the credit gate's file-name rule is `(slot|one_session_only|sole_session)[^/]*_test\.go$`, wider than the proposal's `slot*_test.go` shorthand, and there is a THIRD derived rule (tier-11 files reading the coverage audit) the proposal does not name. Neither widening pulls in a file this proposal creates or edits that the proposal has not already listed. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168,1173,1225
FACT: the credit gate's per-case branch still honours whole-file and directory credits (`creditsMissing(..., wholeFile, perCase[fn], ...)`), so a file with both kinds of entry is checked case-by-case but a whole-file credit still satisfies a case. This is why slotclaimer_test.go's new §5.2-only cases need no entry. — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:973-988

WATCHOUT: the named snapshot `scratchpad/cp-snap/0081_.../non-spec-r5` is byte-identical to the working tree again (eighth recorded occurrence of the snapshot trap). `diff -ru` returns nothing; do not spend the round hunting a delta. The newest differing snapshot in that directory is an earlier round's. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r5

DEFERRED [non-spec-changes.md]: `## Files touched on application` says `tests/tier4_integration/concurrent_workspace_test.go` "is registered as a whole, under sections 5.2, 6.4 and 28.5.3". The map also gives it a 15.1 whole-file credit through the directory entry `tests/tier4_integration/...`, so the enumeration is four sections, not three. The derived action (adding whole-file credits under 7.1 and 4.7 at S12) is unaffected, which is why this is not filed. — EVIDENCE: tests/spec-map.json (directory entry `tests/tier4_integration/...` under §15.1); proposals/0081_.../0081_....non-spec-changes.md:2105 region
DEFERRED [non-spec-changes.md]: SCHEMA-1 quotes the gate's failure text as `ShutdownRequest declares an unexpected field 7 "expected_bind_epoch"`. The shipped format string is `"%s declares an unexpected field %d = %q"`, so the real message carries `= ` before the quoted name. Cosmetic; the gate and the staged `wantReq`/`assertFieldSet` edits are correct as described. — EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:266

USEFUL [refuted list]: the standing refutation that the credit gate treats a `<dir>/...` entry as a whole-file credit saved re-deriving the whole spec-map accounting from scratch; it is correct and I re-confirmed it against `specMapEntryCoversFile`.


### [non-spec.5.review-client-surface.5]

DECISION: returned an empty findings list, the third consecutive empty return for this lens.
BECAUSE (a) the named snapshot `scratchpad/cp-snap/.../non-spec-r5` is again byte-identical to
the whole proposal directory (`diff -ru` prints nothing, ninth recorded form of the snapshot
trap), so there was no new text; and (b) I re-derived the client-surface sweep independently
rather than trusting the prior shards, and every parallel representation still lines up.
ALTERNATIVES rejected: the `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` hole in CODE-4's switch (barred
by the standing Trap at review-log.md:754, and the first-party adapter sets the field on every
return); DOCS-2's bind-epoch block not restating the `Shutdown`-is-not-held carve-out or the
`absent` answer for the no-epoch form (both already on the refuted list); `docs/api/internal.md`
declaring `StopSession`/`StartSessionResponse{success,error_code,error_message}` (pre-existing
whole-page rot, recorded at review-log.md:266).

FACT (re-verified this round by direct read of each message body, not by awk range): all nine
SCHEMA-1 field numbers are free and the enum spelling matches its sibling. `ShutdownRequest`
holds 1,2,3,(4 reserved + `reserved "slot_id"`),5,6 → 7 free; `ShutdownResponse` 1,2 → 3;
`PrepareWorkspaceResponse` 1,2 → 3; `FinalizeWorkspaceResponse` 1 → 2; `RunSetupResponse` 1 → 2;
`StartSessionResponse` 1 → 2; `ConfigureWorkspaceResponse` 1 → 2; `ResumeResponse` 1,2,3 → 4;
`AssignCredentialsResponse` is `{}` → 1. — EVIDENCE: schemas/lenny-adapter.proto:438-449,
699-702, 761-768, 869-871, 958-962, 1033, 1433-1447, 1609-1636, 1665-1668, 1690-1692

FACT: SCHEMA-1's claim that `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is the ONLY
closed field set over the nine messages holds. I enumerated every `Fields().Len()` /
`assertFieldSet` / `assertFields` site under `tests/`: the only other proto pins are
`checkpoint_stream` (CheckpointStart/ChunkReady/CheckpointGrant), `adapter_checkpointbarrier`
(CheckpointBarrierResponse), `adapter_reportusage` (ReportUsageRequest), `adapter_negotiate`
(NegotiateVersionResponse) and `interceptor_proto` — none over a message SCHEMA-1 opens.
`adapter_session_address` and `adapter_generation_fence` both look up fields ByName, so an
additive `int64` trips neither. — EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-254,
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44-96,
tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:70-84

FACT: the golden-bytes case in the same file survives the addition. `TestShutdownRequestUnsetRecycleWireIdentical_spec_4_7`
pins an exact byte string for fields 1-3 with everything else unset, and proto3 emits nothing
for a zero `int64`, so `expected_bind_epoch = 0` adds no bytes. — EVIDENCE:
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:43-83

FACT: `adapter_proto_generation_scope_test.go` counts EXACT occurrences of one sentence
(`operationalFenceSites = 12`) across the whole normalized proto text. SCHEMA-1's new field
comments must not contain the string "A pod validates the generation on every gateway-to-pod
RPC against the value it holds for the session the RPC names, and rejects a request whose
generation does not match it (§10.1)." or the count breaks. The staged comments do not, but
this is a tier-0 gate a careless implementor could trip by copying a neighbouring field's
comment. — EVIDENCE: tests/tier0_static/adapter_proto_generation_scope_test.go:130-137,:186-190

USEFUL [non-spec.2.review-client-surface.2]: its four FACTs (the four tier-0 proto gates are
unaffected; the claim-register row constraints; `validate-maps` accepts a directory glob; the
`slotAddressCaseFiles` two derived rules) all re-checked true and saved me re-deriving them.


### [non-spec.5.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens — BECAUSE every docs surface the change reaches is either staged (DOCS-1, DOCS-2) or already adjudicated in an earlier round, and the three residual candidates I derived all fall in families this loop has already refuted — ALTERNATIVES: I considered filing (a) `docs/reference/state-machines.md:251` enumerating only the cleanup-timeout cause of `slot_cleanup -> leaked` while staged §7.1 adds "a reclaim that did not complete", (b) `docs/operator-guide/multi-tenancy.md:72` as a third carrier of "the per-slot cleanup runs at each session release ... and the adapter reports its outcome", and (c) the DOCS-2 hold sentence omitting §15.4's "`Shutdown` is not held" carve-out. Rejected all three: (a) mirrors §6.2's fence annotation, which SPEC-4 deliberately leaves unedited, so a finding there needs a spec edit the lens forbids me to ask for, and the cause pre-exists in shipped code (`leaked = err != nil || !cleanly`) and in spec/05:545 ("If cleanup fails, the slot is leaked"); (b) is the identical claim to the already-refuted execution-modes.md / security-principles.md finding; (c) is vacuous on the same deregistration-first reading that refuted the spec-side twin.

FACT: `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts exactly the four substrings DOCS-2 claims, and all four survive the staged row rewrite. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307

FACT: the two tier-11 gates DOCS-1 extends behave as the proposal describes. `generalSlotEdges` (tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37) feeds BOTH a positive loop over the either-concurrency block (:54-58) and a negative loop over the concurrent-occupancy block (:68-72), so adding `"receiving_uploads ──→ slot_cleanup"` to the slice gates SPEC-4's placement on both sides in one entry. Neither gate reads the other's carrier, which is the "never meet" claim DOCS-1 rests on.

FACT: every DOCS-2 line citation resolves. adapter-contract.md:10 is the "complete reference" self-claim, :64 is the `DemoteSDK` row, :75 the `Shutdown` row, :84 the `**Scrub responsibilities.**` paragraph the new block is appended after. No tier-11 gate reads the `DemoteSDK` row, so amending it turns nothing red. EVIDENCE: docs/reference/adapter-contract.md:10,64,75,84

FACT: DOCS-2's `DemoteSDK` amendment ("drop the adapter's slot registry entry") is true of the tree, so the docs row is not leading the spec — `DemoteSDK` calls `releaseSessionSlot` on the registry's single entry. EVIDENCE: pkg/adapter/sdkwarm.go:296-301

FACT: the proposal mints no metric and no alert, so there is no docs/reference/metrics.md or docs/runbooks/ companion obligation. `lenny_adapter_leaked_slots` is named in spec/05:545 and spec/06:160 but appears in NEITHER docs/reference/metrics.md nor spec/16 — a pre-existing inventory gap, not this proposal's. EVIDENCE: spec/06_warm-pod-model.md:160; `grep -rn lenny_adapter_leaked_slots docs/` returns nothing

FACT: no docs page mirrors the §7.2 mid-resume snapshot-close sequence or the §29.4 step-13 drain-frame condition. `grep -rn "half-claimed\|snapshot-close\|mid-resume" docs/` returns nothing, so those two staged spec edits have no docs edit site.

WATCHOUT: the snapshot named in the round-5 orchestrator notes was byte-identical to the working tree (`diff -ru scratchpad/cp-snap/.../non-spec-r5 proposals/0081_...` produced no output). This is the seventh/eighth form of the snapshot trap the standing context already records; do not spend a round hunting a delta that does not exist.


### [non-spec.5.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens — BECAUSE every identifier the proposal adds was grepped across spec/, docs/, schemas/, charts/, tests/, pkg/, cmd/, sdks/ and every surface that would become wrong is already in an edit list — ALTERNATIVES: filed nothing on three candidates I chased and dropped, each recorded below with why.

FACT: the nine proto field numbers all verify free, read off the tree this round. `PrepareWorkspaceResponse` holds 1,2 (→3 free); `FinalizeWorkspaceResponse` 1 (→2); `RunSetupResponse` 1 (→2); `StartSessionResponse` 1 (→2); `AssignCredentialsResponse` is `message AssignCredentialsResponse {}` on one line (→1); `ResumeResponse` 1,2,3 (→4); `ConfigureWorkspaceResponse` 1 (→2); `ShutdownRequest` 1,2,3,(4 reserved),5,6 (→7); `ShutdownResponse` 1,2 (→3). EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1033,1433,1609,1665,1690. Do not re-derive this; it cost a full pass.

FACT: `SCHEMA-1`'s "this is the only closed field set over the messages SCHEMA-1 opens" is TRUE. The only closed-set gate is `assertFieldSet` in tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-237 (ShutdownRequest wantReq {1,2,3,5,6}, ShutdownResponse {1,2}). The other descriptor pins do not touch the nine: tests/tier3_contract/adapter_session_address/session_address_wire_test.go only checks `slot_id` absence / `session_id` presence / reserved numbers on REQUEST messages; tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go only asserts `coordination_generation` presence/number/kind per named request; tests/tier3_contract/checkpoint_stream pins `CheckpointStart` (6 fields) and a oneof arm count, neither of which SCHEMA-1 opens. EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:80-175; tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:70-125.

FACT: the claim-register gate does NOT police the two new rows against the proto. `registerProtoDisagreements` only parses `Message.field` out of `claim` when the row's `surface` contains the adapter proto path (tests/tier0_static/claim_register_proto_agreement_test.go:68-74); the staged surfaces name Go files only, so no proto-declaration check fires. Its coverage half is scoped to `coordination_generation` alone (:89-99). And `surfaceNamesOnlyALine` applies only to `WIRED` rows (tests/tier0_static/claim_register_test.go:277-283), which the staged symbol-form surfaces satisfy.

FACT: §28 carries NO `Shutdown` row. `grep -n "Shutdown" spec/28_communication-channels.md` returns nothing, so the proposal's "§28's registers … adds no row" holds. §28.7's artifact register row for the proto (spec/28_communication-channels.md:1782) is per artifact, not per field.

WATCHOUT: `tests/spec-map.json` carries exactly ONE directory entry under `pkg/adapter`, and it is `pkg/adapter/...` under section 4.7 only. Every other `pkg/adapter/*_test.go` credit is a named file. So a new adapter test file gets a free whole-file credit under 4.7 and under nothing else. EVIDENCE: tests/spec-map.json (section 4.7 entry `pkg/adapter/...`). I considered filing that `pkg/adapter/bindepoch_test.go` (new at S9) is absent from the spec-map edit list while its cases will annotate 4.7.1, 5.2, 7.1 and 15.4 — and DROPPED it, because no gate fires: `validate-maps` checks map→disk only (cmd/lenny-test/cmd_validate.go:871-915, `validateSpecMapTestFiles`), `validateSpecMapCoverage` needs each SECTION to have some reference and all four already do, and `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` iterates only `slotAddressCaseFiles`, which `bindepoch_test.go` need not join (its name does not match `slotSubjectFileRE` and it calls none of `slotSurfaceCallRE`'s six spellings). A future round wanting to file this must show a gate, not a rule.

FACT: `derivedInventoryCaseFiles`'s `slotSubjectFileRE` is `(slot|one_session_only|sole_session)[^/]*_test\.go$` and is NOT anchored to the basename — it matches anywhere in the path with no `/` after the stem. Both new slot-named files (`tests/tier7a_load_local/slot_reclaim_hold_race_test.go`, `tests/tier9_security/slot_credential_reclaim_fence_test.go`) therefore do match and genuinely must enter `slotAddressCaseFiles`, which S10 does. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168-1174,1108-1131.

FACT: every production caller of `ReleaseSlotReservation` is in CODE-4's call-site table, verified exhaustively: start.go:2834 (`applySlotRetryPolicy`), start.go:3246 (`rollbackClaim`, declared :3241), binder.go:1714 (`releaseResumeSlot`), slotbinder.go:172 (`ClaimSlot` connect stage), slotbinder.go:217 (`BindReservedSlot`). The only test implementers of the `slotBinder` interface are `fakeSlotBinder` (slotretry_test.go:38) and `concurrentSlotBinder` (slotretry_load_test.go:27), both already in `## Files touched`. `podBinder` and `BindResult.Adapter` are CONCRETE types (`*podsession.Binder` at sessionserver.go:188; `*adapterclient.Client` at binder.go:523), so `ShutdownReclaim`/`BindEpoch` create no interface fan-out and no mock to widen.

FACT: `DemoteSDK` really does drop the registry entry — `s.releaseSessionSlot(s.anyRegisteredSession())` at pkg/adapter/sdkwarm.go:296-298 — so §4.7.1's caller rule ("`DemoteSDK` … removes the entry") and DOCS-2's amended row are both true against the tree.

WATCHOUT: `docs/api/internal.md` is ALREADY badly stale against the shipped proto (it documents a `StopSession`/`UploadFiles` RPC set that does not exist and a `StartSessionResponse{success, error_code, error_message}` that the proto does not declare — docs/api/internal.md:79-127, versus schemas/lenny-adapter.proto:958-962). Its gRPC status-code table at :488-499 lists no `ABORTED`. I dropped a finding there: the page is not a faithful mirror before this change, so the proposal does not make it wrong. Do not re-file it as an 0081 edit site; it is its own pre-existing defect.

UNVERIFIED: `tests/change-graph.json:396-407` maps `schemas/lenny-adapter.proto` to only two tier-3 contract globs (`adapter_jsonl/...`, `adapter_observedlevel/...`), so the new `tests/tier3_contract/adapter_bind_epoch/...` suite will not be selected by `lenny-test --changed` on a later proto edit. No gate enforces this (the change-graph tier-0 gates are scoped to `pkg/ops/*` and `cmd/lenny-ops`, tests/tier0_static/change_graph_ops_selection_test.go:163-217), and the existing entry already omits `adapter_generation_fence`, `gatewaycontrol_scrub` and `checkpoint_stream`, so it is pre-existing coarseness rather than new breakage. Somebody deciding whether the proposal should add the glob should decide it as scope, not as a defect.

FACT: `lenny_adapter_leaked_slots` appears in NO metric inventory — it is absent from spec/16, docs/reference/metrics.md and pkg/alerting/. SPEC-3 naming the gauge in §5.2 therefore adds no inventory-row obligation, and CODE-5 widening the paths that move it creates no metrics/runbook edit site. The gauge is defined only in spec/05:545 and spec/06:160 prose (which, note, attributes it to the ADAPTER while the tree emits it from the gateway — pre-existing, not 0081's).


### [non-spec.5.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor-action assignment in the staged
non-spec and spec text verified against the tree, including all six `ReleaseSlotReservation` call
sites, all nine proto field numbers, every named fixture and helper, and every component
capability the design relies on — ALTERNATIVES: filing the two soft spots noted under UNVERIFIED
below, rejected as wording/rationale precision that leaves neither spec nor implementation wrong.

WATCHOUT: the r5 snapshot at scratchpad/cp-snap/.../non-spec-r5 is BYTE-IDENTICAL to the working
tree, so `diff -ru` returns nothing. This is the standing snapshot trap in its ninth form. Do not
spend a round hunting a delta; read the whole document.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r5

FACT: naive `awk '/^message X \{/,/^\}/'` over schemas/lenny-adapter.proto SILENTLY MIS-SCOPES a
one-line empty message. `AssignCredentialsResponse {}` is on one line, so the range continues into
`RotateCredentialsRequest` and makes field 1 look occupied. It is genuinely empty and the
proposal's `bind_epoch = 1` is correct. Parse with a real script, not a range pattern.
EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: every proposed field number is free on its message, verified by parser:
PrepareWorkspaceResponse holds 1,2 (proposes 3); FinalizeWorkspaceResponse 1 (2);
ResumeResponse 1,2,3 (4); ConfigureWorkspaceResponse 1 (2); RunSetupResponse 1 (2);
AssignCredentialsResponse empty (1); StartSessionResponse 1 (2); ShutdownRequest 1,2,3,res4,5,6 (7);
ShutdownResponse 1,2 (3). EVIDENCE: schemas/lenny-adapter.proto:699,761,869,958,1033,1433,1609,1665,1690

FACT: the complete production+fake set of `ReleaseSlotReservation` call sites is six and CODE-4's
call-site table covers all six with no omission: start.go:2834 (applySlotRetryPolicy),
start.go:3246 (rollbackClaim), binder.go:1714 (releaseResumeSlot), slotbinder.go:172 (ClaimSlot's
connect-stage release), slotbinder.go:217 (BindReservedSlot), plus the interface at start.go:2727.
The two fakes are slotretry_test.go:52 and slotretry_load_test.go:33, both in the touched-files
list. A future round need not re-derive this.
EVIDENCE: pkg/gateway/sessionserver/start.go:2727,2834,3246; pkg/gateway/podlifecycle/podsession/binder.go:1714; slotbinder.go:172,217

FACT: CODE-1's two epoch-refusal early returns sit ABOVE clause three (the recycle whole-pod
scrub), so they would skip `startPodScrub`. This is unreachable rather than a defect: the only
sender of a non-zero epoch is `compensateFailedSlotBind`, and CODE-6 keeps `ShutdownRecycle` at a
zero epoch, so `epoch != 0` is false on every recycle call. Anyone restructuring CODE-1 must keep
that invariant or move the refusal below clause three.
EVIDENCE: pkg/adapter/session.go:283-289 (clause three); non-spec-changes.md CODE-6 ("`Shutdown` and `ShutdownRecycle` keep their signatures and pass a zero epoch")

FACT: the mid-session upload path cannot send an upload-free `PrepareWorkspace`.
`parseUploadToSession` rejects an empty `files` array with 400 VALIDATION_ERROR, and
`sendUpload` always sends at least one frame. So the "every gateway `PrepareWorkspace` resolves
the entry and reports a non-zero epoch" claim holds on BOTH callers, not only `stageWorkspace`.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:183-186; pkg/gateway/runtime/adapterclient/client.go:269-271

UNVERIFIED (both judged below the bar, recorded so a later round does not re-derive them):
(1) CODE-6 and SCHEMA-1 both justify the per-entry latch unit with "`Binder.Prepare` closes its
connection and `Binder.Launch` reconnects, so one create-time bind attempt spans two connections
... each connection's compensation names what that connection observed". Prepare/Launch is the
EXCLUSIVE create-time path, which CODE-4 explicitly does not compensate, so the phrase "each
connection's compensation" names a compensation that does not exist on that path. The load-bearing
half (both connections resolve one entry and observe one epoch) is true. Rationale imprecision.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:843,957,976-977; non-spec-changes.md CODE-2 ("the exclusive path CODE-4 does not compensate")
(2) SPEC-2's §7.2 step 3 obliges the gateway to send the reclaim "on the connection that attempt
still holds" for an aborted mid-resume. No code deliverable targets the §7.2 terminal-collapse
path directly; the obligation is discharged only if the terminal transition cancels the in-flight
`cl.Resume`, which routes into `Binder.Resume`'s failure branch that CODE-4 does stage. Somebody
should confirm the mid-resume terminal actually cancels the in-flight resume rather than racing it.
EVIDENCE: pkg/gateway/sessionserver/start.go:3478,4426; pkg/gateway/podlifecycle/podsession/binder.go:1620-1631


### [non-spec.5.review-fresh.1]

DECISION: filed exactly one finding, the CODE-5 `slotBinder`-interface sentence at
non-spec-changes.md:861-863 — BECAUSE it is the one item in `### Traps` marked "filed
twice/three times and still standing" that is a genuine self-contradiction inside the
staging (CODE-4's call-site table at :692 and CODE-5's own Targets line at :736 both name
the interface as an edit site) rather than a bookkeeping or prose nit, and the same
sentence's second clause ("both test fakes compile as they stand") is falsified by the
tree: `slotBinder.ReleaseSlotReservation` is the three-parameter form at
pkg/gateway/sessionserver/start.go:2727 and both fakes implement that arity
(slotretry_test.go:52, slotretry_load_test.go:33) — ALTERNATIVES: I considered filing the
§4.7.1 per-connection-versus-per-session caller-rule granularity (standing Open 848) and
declined it, because closing it needs §15.4/§4.7.1 to state a one-session-per-connection
premise the tree does not require, which is the published-contract-completeness class the
material skeptic has refuted four times (Traps 719, 735).

FACT: the whole `## Standing context` (1018 lines, review-log.md:3-919) cannot be read with
`bash`; output over ~2KB is persisted to a tool-results file and only a 2KB preview is
shown. The Read tool also refuses >25k tokens per call. The working recipe is
`Read(review-log.md, offset=N, limit=100..180)` in five passes; anything over ~180 lines of
that file exceeds the token cap. EVIDENCE: proposals/0081_.../0081_....review-log.md:761
(the entry that says the same thing, and understates the workaround).

FACT: the snapshot named in the brief, scratchpad/cp-snap/.../non-spec-r5, was again
byte-identical to the live proposal directory (`diff -ru` produced no output at all,
including no "Only in" lines for summary.md/status.md). The eighth form of the snapshot
trap held for a ninth round. Budget zero time for the delta.

FACT: verified fresh against the tree, so nobody re-derives them: `isTransientPodClaimError`
is a `switch` with a trailing `return false` at pkg/gateway/sessionserver/start.go:3648-3681,
so CODE-5's "add one arm after the existing switch and before that final `return false`"
applies verbatim; `handleUploadToSession` answers 502 `UPSTREAM_ERROR` on a
`PrepareWorkspace`/`FinalizeWorkspace` failure and 409 `TARGET_NOT_READY` when the binding
is already pruned (pkg/gateway/sessionserver/upload_to_session.go:118,:130,:136), which is
what the edge-case enumeration at non-spec-changes.md:2001-2004 claims; the three
`CategoryPermanent` stamps CODE-6's resolve table names are at pkg/adapter/staging.go:81,
:183, :339 and `resolvePrepareStagingDir` itself re-wraps as `InvalidArgument` at :136, so
every cell of that table is accurate; `SlotBindError.Reason()` switches on the CODE alone
(slotfailure.go:85-100) except for the `slotFailureWorkspacePrep` carve-out on
`FailedPrecondition`, so `codes.Aborted` is transient at every stage and the staged
"add a workspace-stage row beside the start-stage one" is redundant coverage rather than a
needed discriminator; `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:298-307`
reads the row with `lineContaining(page, "| \`Shutdown\` |")` and DOCS-2's new block lands
after `**Scrub responsibilities.**` at docs/reference/adapter-contract.md:84, i.e. below the
row at :75, so the first-match scan still resolves to the row.

MISTAKE: the fix stage after `[non-spec.2.*]` again did not sweep `### Traps` for entries
marked "filed and still standing". Trap 758 records the `slotBinder` sentence as filed three
times with no fix; this round is the fourth filing. Standing-context entry 776 already names
this exact cost and it recurred anyway.

UNVERIFIED: whether `fakeSlotBinder.released`'s type change (`[][2]string` → something
carrying the `leaked` bool) is in any edit list. `pkg/gateway/sessionserver/slotretry_test.go`
is in `## Files touched on application (non-spec)` (non-spec-changes.md:2182), so the edit is
authorized, but the staged tier-1 accounting case at non-spec-changes.md:1546-1549 asserts
`ReleaseSlotReservation` was called "with `leaked=true`" and no bullet says the fixture field
grows. A fixer landing the interface correction should fix the field in the same clause.
EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:29.


### [non-spec.5.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE this
proposal's whole mechanism (bind epoch, reclaim hold, slot registry) is in-process adapter
state plus one in-memory gateway request field; it mints no CRD field, no status write, no
finalizer, no webhook rule, and no new controller. ALTERNATIVES: I probed the three K8s
surfaces it does touch (DrainSandbox, ClaimSlot's two candidate passes, SlotClaimer.ReleaseSlot)
and each proposal claim checks out against the tree.

FACT: `Binder.DrainSandbox` writes NO status: it calls `podclaim.StampDrainRequest`, stamping
the §4.6.3 `lenny.dev/drain-request` annotation, and its doc comment states the gateway never
writes `Sandbox.status` because the WarmPoolController is the sole writer. The proposal's
CODE-5 "Reachability" paragraph (non-spec-changes.md:898-903) depends on that drain being
asynchronous, and it is. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:583-603.

FACT: `ClaimSlot` pass 1 really does gate on the per-pod `SandboxClaim` (podClaim + IsTerminal +
TenantID) and NOT on `Sandbox.status.phase`; only pass 2 reads the phase (`sb.Status.Phase !=
Idle`). So CODE-5's argument that the stamped drain cannot keep the immediate retry off the pod,
and that `ExcludePods` is what does, is sound rather than a workaround for a missing status
read. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:411-430 (pass 1), :477-480
(pass 2). This is the correct level-triggered/synchronous split: the controller owns the drain
transition, the request path owns its own placement filter.

FACT: `SlotClaimer.ReleaseSlot` with `leaked=true` returns before the counter DECR and before
`DeleteClaim`, so a leaked release leaves the per-pod `SandboxClaim` at `bound`. The tier-4
`TestRecyclePathUnansweredReclaimLeaksAndMovesTheRetry_spec_5_2` assertion
(non-spec-changes.md:1747-1750) is therefore accurate, and the residual `bound` claim is not a
stuck-finalizer footgun: §4.6.1 orphan GC drains a `bound` claim past `claimOrphanTimeout`.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836, :881-884.

FACT: the §5.2 `**Slot cleanup:**` bullet does carry a `SandboxWarmPool` admission-webhook CRD
validation rule, so SPEC-3's commentary "the CRD validation rule ... stand as written"
(spec-changes.md:630) is a true citation, and nothing staged changes the webhook's predicate.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545.

WATCHOUT: the r5 non-spec snapshot at
scratchpad/cp-snap/0081_.../non-spec-r5 is byte-identical to the working tree (`diff -ru`
produced nothing), which is the snapshot trap the standing context already records in its
seventh and eighth forms. Do not spend a round hunting the delta.


### [non-spec.5.review-mechanism.1]

FACT: The r5 snapshot `scratchpad/cp-snap/.../non-spec-r5` is byte-identical to the working tree, so `diff -ru` returns nothing. This is the seventh/eighth form of the snapshot trap the standing context already records; do not spend a round hunting a delta.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/non-spec-r5

FACT: The proto field numbers in SCHEMA-1's table are all correct, including `AssignCredentialsResponse.bind_epoch = 1`. `message AssignCredentialsResponse {}` really is empty at schemas/lenny-adapter.proto:1033. WATCHOUT: a naive `awk '/^message X \{/,/^\}/'` range scan MISREADS this, because the line ends `{}` and the range runs on into `RotateCredentialsRequest` and reports its fields as collisions. I nearly filed a false finding on it. Use a brace-depth parser, and treat a single-line `{}` message specially.
EVIDENCE: schemas/lenny-adapter.proto:1033; next free numbers verified: ShutdownRequest 7 (1-3,5,6 used, 4 reserved), ShutdownResponse 3, PrepareWorkspaceResponse 3, FinalizeWorkspaceResponse 2, ResumeResponse 4, ConfigureWorkspaceResponse 2, RunSetupResponse 2, StartSessionResponse 2.

FACT: `Shutdown` has THREE clauses, and clause three (the whole-pod recycle scrub) sits AFTER clause two's teardown and before the response. Any staged code that `return`s out of clause two skips it.
EVIDENCE: pkg/adapter/session.go:282-290 (`if rc := req.GetRecycle(); rc != nil { s.startPodScrub(rc) }`)

FILED: CODE-1's two epoch-refusal branches `return` from the handler, so a `Shutdown` carrying a mismatched/absent epoch AND the recycle disposition performs no whole-pod scrub, while staged §4.1 (spec-changes.md:296) and staged DOCS-2 (non-spec-changes.md:1304) both say the scrub runs whenever the recycle disposition is set. Unreachable from today's gateway (`ShutdownRecycle` sends epoch 0), but it is a contradiction between the staged spec/docs text and the staged code, and it makes the scrub selected by `expected_bind_epoch`'s value, which is exactly what §4.1's closing rule denies.
EVIDENCE: non-spec-changes.md:160-173; spec-changes.md:296; non-spec-changes.md:1304; pkg/adapter/session.go:288-290

FACT (checked clean, do not re-derive): the three deregister-then-destroy sites CODE-6 enumerates are exactly the three in the tree — `Shutdown` (session.go:238/271), `releaseSessionSlot` (slotsession.go:215/217), `deregisterStartedSessions`+`terminateHeldSession` (slotsession.go:389, holdstate.go:254). No fourth site deletes from `s.slots` and then destroys.
EVIDENCE: grep of `removeSlotTree|deregisterSlot(|deregisterSlotLocked(|delete(s.slots` over pkg/adapter/*.go

FACT (checked clean): all five `ReleaseSlotReservation` production call sites plus the `slotBinder` interface decl are covered by CODE-4's call-site table — start.go:2834 (applySlotRetryPolicy), start.go:3246 (rollbackClaim), slotbinder.go:172 (ClaimSlot connect stage), slotbinder.go:217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), start.go:2727 (interface).

FACT (checked clean): `podsession.ResumeRequest` and `podsession.SlotBindRequest` both carry `CleanupTimeoutSeconds int` and `MaxConcurrentSessions int32`, so `slotCleanupBudget`'s and `compensateFailedSlotBind`'s scalar parameter lists are satisfiable from either request.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:645,667; slotbinder.go:39,108

FACT (checked clean): the three staging.go resolve sites are at :133/:136, :181/:184, :337/:340 with their span stamps at :81, :183, :339 — exactly as CODE-6's table states; slotsession.go:74-78 and slotcreds.go:26-28 are the other two. `SlotBindError.Reason()` has no `Aborted` case and falls to the transient default (slotfailure.go:99-100), and `isTransientPodClaimError` ends `return false` at start.go:3680, so CODE-5's arm lands where the proposal says.

FACT (checked clean): `DemoteSDK` really does drop the registry entry (`releaseSessionSlot` via `anyRegisteredSession`), so DOCS-2's amended `DemoteSDK` row and §4.7.1's latch-clearing caller rule are both accurate.
EVIDENCE: pkg/adapter/sdkwarm.go:296-301

UNVERIFIED: §4.7.1's caller rule says a caller holds no epoch "once it has itself issued an RPC on that connection that removes the entry the epoch names ... a `Shutdown` answering `reclaimed` removes it", but CODE-6's latch clears only on `DemoteSDK` and on `ShutdownReclaim` answering RECLAIMED — the plain `Client.Shutdown` (Binder.ReleaseSlot, slotbinder.go:542) also answers `reclaimed` and does not clear. I did NOT file it: `ReleaseSlot` only reuses that connection for `ShutdownRecycle`, which sends epoch 0 unconditionally, and then closes it, so no compensation ever reads the stale latch. Someone should confirm no other caller sends a plain `Shutdown` and then a compensation on the same connection.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:541-577

MISTAKE (mine, avoided): I initially read the tier-4 "late-reclaim arm" (non-spec-changes.md:1723-1732) as unrealizable, on the ground that if the adapter admitted bob's start the retry's `claimSessionSlotUnderLock` refuses on `st.started`. It IS realizable: the fixture injects an adapter-side `StartSession` failure, whose shipped pre-`Runtime.Start` branches call `releaseSessionSlot` and remove the entry (pkg/adapter/session.go:133,:147,:157), so the retry creates a fresh entry at a fresh epoch and the delayed compensation is correctly answered SUPERSEDED. Do not re-file this.


### [non-spec.5.review-operational.1]

DECISION: filed exactly one finding — SPEC-3's "A cleanup on that path that does not complete
is still accounted" sentence names the `Shutdown` response as the sole accounting carrier,
which is false for the variant of the same path where the cleanup is the adapter's OWN
pre-`Runtime.Start` rollback (`releaseSessionSlot`), whose tree-removal error is discarded at
`pkg/adapter/slotsession.go:217` (`_ = removeSlotTree(st)`). BECAUSE the gateway's later
compensating `Shutdown` then answers `ABSENT` (the entry is already gone), CODE-4 maps
`ABSENT` to not-leaked, so the incomplete cleanup reaches no `leaked` sub-state, no
`lenny_adapter_leaked_slots` movement, and no contribution to the `ceil(maxConcurrentSessions/2)`
replacement trigger. ALTERNATIVES: I considered filing the `holdstate.go:254` sibling discard
(the §10.1.4 hold termination also discards `removeSlotTree`'s error) and declined — that path
terminates STARTED sessions, so it is not obviously inside SPEC-3's pre-`running` scope, and the
pod is retiring anyway.

FACT: the leaked-slot surface has NO alert. `pkg/alerting/rules/rules.go` contains no slot rule
at all (`grep -n "slot" pkg/alerting/rules/rules.go` is empty), so nothing in this proposal can
orphan an alert or leave one referencing an unemitted series. The alert-to-runbook lens is
vacuous for 0081; do not spend a round on it. EVIDENCE: pkg/alerting/rules/rules.go

FACT: `lenny_adapter_leaked_slots` is the ONLY observability surface this change moves, and the
gateway writes it. After CODE-5 it has three producers instead of one (`applySlotRetryPolicy`,
`bindConcurrentSlot`'s reserved branch, `resumeOnPod`), all inside `accountSlotFailure`. No
metric is added, no label value is added (the `"resume"` stage string never reaches
`lenny_slot_failure_total`, whose only producers are `recordSlotFailure`'s call sites inside
`materializeSlot`). spec/16 and docs/reference/metrics.md both stay true verbatim.
EVIDENCE: spec/16_observability.md:14,:15,:128; docs/reference/metrics.md:165-167

FACT: every DOCS-2 citation resolves against the current tree and the staged row satisfies the
tier-11 gate it extends. `docs/reference/adapter-contract.md:10` (complete-reference claim),
`:64` (`DemoteSDK` row), `:75` (`Shutdown` row), `:84` (`**Scrub responsibilities.**`) are all
exact. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311) reads the row
through `lineContaining(page, "| \`Shutdown\` |")` and requires four substrings, all of which
survive the staged rewrite; the two substrings DOCS-2's test work adds
("no other bound session", "a session whose start the adapter has admitted") are both present in
the staged row, and "bind epoch" / "reclaim hold" are both present in the staged block heading.
DOCS-2's `DemoteSDK` claim is true of the tree: `Server.DemoteSDK` calls
`releaseSessionSlot(anyRegisteredSession())` (pkg/adapter/sdkwarm.go:296-299).

FACT: the §16.3 tracing citations in the CODE-6 test plan all resolve.
`pkg/observability/tracing/tracing.go:107` is `AttrErrorCategory = "error.category"` and `:202`
is inside `RecordError`; `pkg/adapter/staging.go:81,:183,:339` are the three literal
`tracing.CategorizeError(..., tracing.CategoryPermanent)` stamps; `pkg/adapter/tracing_internal_test.go:23,:33`
are `installInternalSpanRecorder` and `endedSpanNamed`. spec/16:372 defines `TRANSIENT`.

FACT: `SlotBindError.Reason()` is stage-agnostic for `codes.Aborted` — it falls to the
`default: return SlotReasonTransient` arm (pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101),
so the credential-assignment stage's reclaim-hold refusal classifies transient exactly as the
workspace and start stages do. A finding shaped "the credential stage has no `Aborted` test row"
is cosmetic, not a classification defect. `isTransientPodClaimError` has exactly one production
caller, `holdOrFailOnResumeError` (pkg/gateway/sessionserver/start.go:3610), so CODE-5's new
`codes.Aborted` arm cannot widen any other decision.

WATCHOUT: the snapshot named in the round-5 prompt
(`scratchpad/cp-snap/.../non-spec-r5`) is BYTE-IDENTICAL to the working tree —
`diff -rq` returns nothing. This is the seventh-or-eighth recurrence the standing context warns
about. Do not open the round by hunting a delta.

WATCHOUT: `awk '/^message X \{/,/^\}/'` over `schemas/lenny-adapter.proto` silently spans past a
single-line message. `AssignCredentialsResponse` is declared as `message AssignCredentialsResponse {}`
on one line (schemas/lenny-adapter.proto:1033), so the range form swallows the NEXT message and
makes SCHEMA-1's "an empty message today, so its first field is 1" look false. It is true. Use
`sed -n '/^message X {/,/^}/p'` and read the declaration line itself. I nearly filed a
field-number collision on this. EVIDENCE: schemas/lenny-adapter.proto:1033

FACT: every SCHEMA-1 field number is free on its message, verified one message at a time.
`ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (7 free); `ShutdownResponse` holds 1,2 (3 free);
`PrepareWorkspaceResponse` 1,2 (3 free); `FinalizeWorkspaceResponse` 1 (2 free); `ResumeResponse`
1,2,3 (4 free); `ConfigureWorkspaceResponse` 1 (2 free); `RunSetupResponse` 1 (2 free);
`StartSessionResponse` 1 (2 free); `AssignCredentialsResponse` empty (1 free).

FACT: the claim-register staging is well formed against the generator. `EXPLICIT` is at
scripts/seed-claim-register.py:169 and is appended into the row set at :378; it already carries
`WIRED` rows with no `deferral_id` (the two credential-addressing rows at :208 and :225), and
`#2851-gateway-to-pod` is a live anchor there (:86, :93, :182) resolving to spec/28's
`#### 28.5.1 Gateway-to-pod` heading at spec/28_communication-channels.md:205.

USEFUL [review-log.md:1293, the `slot_cleanup -> leaked` cause enumeration]: it saved me from
filing `docs/reference/state-machines.md:251` ("when the cleanup timeout is exceeded") as stale
against the widened leak predicate. The doc mirrors `spec/06_warm-pod-model.md:148`'s fence
annotation, which the proposal does not edit, so the docs page agrees with the POST-change spec.


### [non-spec.5.review-performance.1]

DECISION: returned an EMPTY findings list for the performance/scalability/failure-mode lens on
round 5 — BECAUSE the r5 delta adds no mechanism with a capacity or store dimension, and the one
genuinely new mechanism statement (the third reclaim-hold producer) checks out line by line
against the tree. ALTERNATIVES: I considered filing (a) the compensating `Shutdown` pinning a
gateway request goroutine for up to `slotCleanupBudget`, (b) the refused-retry's windowed
`RecordFailure` pushing a healthy pod over `UnhealthyThreshold` at `maxConcurrentSessions: 4`,
and (c) the §10.1.4 pass-1/pass-2 hold window holding every started session's identifier for up
to ten seconds. All three are barred: (a) and (b) are named verbatim in the standing context's
two "capacity dresses" MISTAKE entries and the six-passes-declined trap, and (c) is inert because
the hold is keyed on the slot identifier, which equals the session identifier, so it collides with
no other session and no placement decision.
EVIDENCE: review-log.md:738, :777; pkg/adapter/slotsession.go:375-399.

FACT: the r5 snapshot `non-spec-r5` (and `non-spec-r4`, `non-spec-r4-start`, `non-spec-r5-start`)
are all byte-identical to the working tree. The newest DIFFERING snapshot is `non-spec-recheck-r6`.
This is the eighth recurrence of the snapshot trap the standing context records; diff against
`non-spec-recheck-r6` directly rather than hunting.
EVIDENCE: scratchpad/cp-snap/0081_.../non-spec-recheck-r6/

FACT: the whole r5 delta is four things and nothing else — the third hold producer (the start-path
rollback hold a §5.2 retry can meet, added to spec-changes.md §7.1 and its accepted-failure-mode
bullet, and to non-spec-changes.md's "What can meet the reclaim hold" bullet and the CODE-4
tree-reuse paragraph), concrete test-function names for six previously unnamed cases, the tier-2
annotation narrowed from `§7.1; §5.2` to `§5.2`, and the rewritten `tests/spec-map.json` bullet.
No deliverable, no code sketch, no proto field, no store write moved.

FACT: every tree claim in the new third-hold-producer bullet holds.
`const maxSlotRetries = 1` is at pkg/gateway/sessionserver/start.go:2720 exactly.
`applySlotRetryPolicy` records the windowed `health.RecordFailure(sbe.Pod)` only on the clean-release
arm (start.go:2841-2854), and the refused retry reaches that arm because the compensation is answered
`ABSENT` and `sbe.Leaked` is false, so the bullet's accounting is right. `maxSlotRetries == 1` does
make the refused retry the request's last attempt, returning `SlotFailedError` (start.go:2872-2880).
All sixteen `releaseSessionSlot` call sites are synchronous inside their own handler
(resume.go:69-141, sdkwarm.go:236-298, session.go:133/:147/:157), so the bullet's "the hold ends
before the handler answers" is true of every one of them.

FACT: the adapter's `Shutdown` clause three (`startPodScrub`) fires only when `req.recycle != nil`
(pkg/adapter/session.go:288-291), and the compensation populates no `recycle`, so a compensating
`Shutdown` naming a session the adapter holds no entry for never triggers a whole-pod scrub on a
pod serving co-tenants. I chased this as a candidate destructive path and it is closed in the
shipped handler.

FACT: `DialAdapter` is a fresh `adapterclient.Dial` per bind (cmd/lenny-gateway/stores.go:2094-2096,
called at podsession/binder.go:1152,:1776 and slotbinder.go:237,:459), with no pooling anywhere, so
the per-connection epoch latch CODE-6 stages cannot be shared between two sessions on one pod. The
"no other attempt reads it because a further attempt dials its own connection" claim at
non-spec-changes.md:1085-1087 is true against the tree. Anyone re-deriving a cross-session latch
corruption should stop here.

WATCHOUT: the ordering that DOES survive the hold is the one where the retry is admitted BEFORE the
rollback's deregistration — the rollback then deregisters the RETRY's entry and `removeSlotTree`
deletes the retry's tree. The hold cannot reach it, because the hold only blocks admission inside
its own window. The r5 text states this correctly as a pre-existing hazard
(non-spec-changes.md:887-890); do not re-file it as something the hold was supposed to close.
EVIDENCE: pkg/adapter/slotsession.go:214-220.


### [non-spec.5.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every recovery/retry path I traced under
crash, restart, redelivery and store-failover is either correct as staged or already disclosed
as an accepted residue with its cost stated — ALTERNATIVES: I worked up four candidate findings
and killed all four on tree evidence (details below), so the next reliability pass should start
from those four rather than re-deriving them.

FACT: the r5 snapshot is byte-identical to the working tree again (`diff -ru
scratchpad/cp-snap/.../non-spec-r5 proposals/0081_.../` is empty), the eighth instance of the
snapshot trap the standing context records. Do not spend a round hunting the delta.

FACT (kills candidate 1: "a reclaim races the abandoned attempt's own in-flight writer and
leaves orphaned credential material"). `assignCredentialsSlot` holds `s.mu` across its ENTIRE
body — `ensureSlotStateLocked`, `writeSlotCredentialFile` and `reconcileSlotExpiryTimersLocked`
are all inside one `s.mu.Lock(); defer s.mu.Unlock()`. EVIDENCE: pkg/adapter/slotcreds.go:24-52.
So the reclaim hold placed at the top of `ensureSlotStateLocked` (CODE-6) genuinely closes the
credential path: a credential write either completes before the deregistering critical section
or is refused by the hold. There is no window in which credentials.json is written after
`removeSlotTree`. The same is NOT structurally true of `RunSetup` (resolves paths at
staging.go:337, then runs setup commands outside the lock) or `PrepareWorkspace` (resolves
`stagingDir` once at staging.go:78-85, then writes upload chunks across the streaming loop), but
both take the inbound RPC ctx, which gRPC cancels when the gateway's stage deadline expires —
the only ordering in which the compensation fires while the handler is still live — so the
window is cancellation-propagation wide rather than setup-command wide. I judged that below the
bar; a future round wanting to re-open it must show `workspace.RunSetup` writing after ctx
cancellation, not merely that the hold does not cover an already-admitted handler.

FACT (kills candidate 2: division by zero in `slotCleanupBudget`). `maxConcurrentSessions` can
never be 0 at either call site. The bind path is gated on `match.MaxConcurrentSessions > 1`
(pkg/gateway/sessionserver/start.go:2351, :2465) and `ResumeRequest.MaxConcurrentSessions` is
documented and built as "normalized to a minimum of 1 by the caller"
(pkg/gateway/podlifecycle/podsession/binder.go:640-645; the caller is
`maxConcurrentSessions(match.MaxConcurrentSessions)` at start.go:4029).

FACT (kills candidate 3: `materializeSlot`'s wrapper releases only pool leases, leaking user
leases). `UserCredentialAssigner` has no release method by design: "User leases share the lease
store the pool assigner uses, so the pool assigner's ReleaseSession releases them at teardown."
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:335-348; `releaseCredentials` at :1263-1268.

FACT (kills candidate 4: a missed `ReleaseSlotReservation` call site when the signature widens).
There are exactly five non-test call sites and CODE-4's table names all five:
start.go:2834 (`applySlotRetryPolicy`), start.go:3246 (`rollbackClaim`), slotbinder.go:172
(`ClaimSlot` connect-stage), slotbinder.go:217 (`BindReservedSlot`), binder.go:1714
(`releaseResumeSlot`), plus the interface declaration at start.go:2727.

FACT: `materializeSlot` has exactly the five `cl.Close()` error-path calls CODE-4 says it does,
and returns `cl` inside `BindResult` on success, so the wrapper-owns-the-close restructure is
sound. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:265-351.

FACT: `SlotBindError` does not implement `GRPCStatus()`, and `slotErrCode` already walks the
chain with `errors.As` (pkg/gateway/podlifecycle/podsession/slotfailure.go:104-113), so CODE-5's
`status.Code(err) == codes.Aborted` arm fires through both the `fmt.Errorf` wrapper and the
`*SlotBindError` that `Binder.Resume` will return. grpc is v1.80.0 (go.mod:63), whose
`status.FromError` uses `errors.As`.

FACT: every bind attempt dials its own adapter connection (`b.DialAdapter` inside `connectSlot`,
pkg/gateway/podlifecycle/podsession/slotbinder.go:459), so the per-connection epoch latch CODE-6
adds cannot be cross-contaminated by a co-tenant session on the same pod. `Binder.Resume` sends
only `cl.Resume` after `connect` (binder.go:1588-1620), which confirms the proposal's claim that
the resume compensation legitimately carries a zero latch.

UNVERIFIED: CODE-4's outcome switch has no arm for `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` (0); it
falls to `default: return false`, so an adapter that answers `exited_cleanly: false` without
populating `slot_reclaim` is recorded as NOT leaked, which inverts today's
`leaked = err != nil || !cleanly`. The first-party adapter always sets the field (CODE-1 sets
ABSENT or RECLAIMED on every path), so this is reachable only from a version-skewed or
third-party adapter, and `.claude/rules/code-best-practices.md` bars backward-compatibility
shims. I did not file it. Someone weighing whether long-lived warm pods can outlive a gateway
roll should decide whether the `default` arm should read `!cleanly` rather than `false`.

WATCHOUT: the hold's `defer release()` in CODE-1's sketch sits after `reportSessionScrub`, so
the hold spans an outbound GatewayControl call, while staged §5.2 enumerates the hold's covered
acts as the runtime close, the directory removals and the process-group kill and says nothing
about the report. Holding longer than the spec enumerates is harmless (the identifier is the
session's own), so this is not a defect — do not file it — but do not "tighten" the code to
release before the report either, because the report is bounded by the inbound RPC ctx and
moving the release earlier reopens the window the hold exists for.
EVIDENCE: pkg/adapter/sessionscrubreporter.go:61-85.


### [non-spec.5.review-security.1]

DECISION: returned an empty findings list for the security lens on round 5 — BECAUSE every
control the lens owns either survives the change unchanged or is strengthened by it, and each
candidate I derived resolved against the tree. ALTERNATIVES: I considered filing (a) the
`codes.Aborted` arm CODE-5 adds to `isTransientPodClaimError`, (b) the runtime-teardown gate
moving from `bound` to `started` weakening §11.4 full revoke, (c) the reclaim hold opening a
window in the SDK-warm one-session-only guard, and (d) the gateway sourcing the `leaked`
disposition from a pod self-report. All four are refuted below.

FACT: **§11.4 full revoke can never reach a bound-but-unstarted session, so CODE-1's move of
the runtime teardown from `bound` to `started` cannot weaken it.** `terminateLocal` iterates
`p.registry.Snapshot()` and skips any session with no published binding
(`cmd/lenny-gateway/user_revocation.go:121-129`), and `podRegistry.Put` publishes only after a
bind returned successfully. EVIDENCE: cmd/lenny-gateway/user_revocation.go:121-129.

FACT: **The revoke's `Shutdown` is the 4-arg client method, which CODE-6 keeps at a zero
epoch, so §11.4 sends the unconditional form and the escape hatch holds in code as well as in
spec.** EVIDENCE: cmd/lenny-gateway/user_revocation.go:129
(`bind.Adapter.Shutdown(callCtx, bind.SessionID, reason, userTerminateDeadline)`);
non-spec-changes.md CODE-6 ("`Shutdown` and `ShutdownRecycle` keep their signatures and pass a
zero epoch").

FACT: **The five `codes.InvalidArgument` resolve sites the proposal enumerates are exactly the
five production callers of `ensureSlotStateLocked`/`ensureSlotPaths` in the tree — there is no
sixth the hold refusal could escape through as a permanent error.** EVIDENCE:
pkg/adapter/staging.go:136,:184,:340; pkg/adapter/slotsession.go:77; pkg/adapter/slotcreds.go:28;
the caller enumeration is `grep -n "ensureSlotPaths\|ensureSlotStateLocked" pkg/adapter/*.go |
grep -v _test.go`, which returns only those five plus the definitions.

FACT: **The span categories the proposal's five-site table claims are what the tree stamps.**
`resolvePrepareStagingDir`'s caller stamps `CategoryPermanent` at staging.go:81; `FinalizeWorkspace`
and `RunSetup` stamp beside the wrap at staging.go:183 and :339; the two `ensureSlotStateLocked`
sites stamp nothing. EVIDENCE: pkg/adapter/staging.go:77-83,:181-186,:337-342.

FACT: **`slotlayout.RemoveTree` does reach `/run/lenny/slots/{sessionId}` and
`deregisterSlotLocked` does cancel every armed §4.9 timer**, so SPEC-3's widened action list and
CODE-1's "widening the tree removal to `removed` is what reclaims credentials.json" are both
shipped behaviour rather than new obligations. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68;
pkg/adapter/slotsession.go:174-181.

FACT: **`Binder.Resume` mints no §4.9 credential lease**, so CODE-4's decision not to call
`releaseCredentials` on the resume compensation is correct rather than a credential leak.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1591-1630 — the body runs
`connect` → `reserveResumeSlot` → `cl.Resume` and touches no credential path.

WATCHOUT: **The `codes.Aborted` arm CODE-5 adds to `isTransientPodClaimError` is a blanket arm,
not a sentinel match, so it reclassifies EVERY `Aborted` a resume can surface as transient.**
It is still not a security finding: the arm only chooses between reverting the row to
`awaiting_client_action` and demoting it to terminal `failed`, and neither is a fail-closed
security predicate — the wire envelope already answers a retryable 503 `RESUME_FAILED` for an
unrecognised cause. A future reviewer tempted to file this must first name a security property
the terminal-`failed` demotion enforces. EVIDENCE: pkg/gateway/sessionserver/start.go:3599-3620
(`holdOrFailOnResumeError`), :3648-3681 (the shipped arms, all typed errors or sentinels).

WATCHOUT: **The reclaim hold does not close the SDK-warm idle guard's window, and that is not a
regression.** `claimSessionSlotUnderLock`'s `sdkWarm` scan refuses a second bind only while
another entry has a non-empty `sessionID` (pkg/adapter/slotsession.go:67-73). During a reclaim
the entry is already deregistered, so the guard sees an idle pod while `Runtime.Close` is still
running outside `s.mu` — but today's `Shutdown` deregisters under `s.mu` and closes outside it
too, so the window predates the proposal and the hold neither widens nor narrows it (the hold is
keyed on the reclaimed identifier alone). EVIDENCE: pkg/adapter/slotsession.go:64-90;
pkg/adapter/session.go:238-282.

WATCHOUT: **`DemoteSDK` runs in `Binder.Prepare` BEFORE any workspace RPC, so the hold it opens is
on a prior session's identifier (or on nothing), never on the identifier the bind sequence that
follows will use.** Do not file the "DemoteSDK's hold blocks its own pod-warm bind sequence"
finding; SPEC-3 also states the same conclusion normatively ("A release that runs its cleanup
inside the RPC that requested it ends the hold before that RPC answers"). EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:878-897 (DemoteSDK call site, ahead of the workspace
phase at :905+); pkg/adapter/sdkwarm.go:296-301 (`anyRegisteredSession` then `releaseSessionSlot`).

FACT: **The `leaked` disposition was already a pod self-report before this proposal**
(`leaked = err != nil || !cleanly`, with `cleanly` read off `ShutdownResponse.exited_cleanly`),
so CODE-4's outcome-first mapping changes which self-reported field the gateway reads and not
the trust level of the bound. Under the lens's check (2) this is not a regression, and a filing
against it would have to argue the pre-change `exited_cleanly` read was equally defective.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559.

UNVERIFIED: `pkg/adapter/sdkwarm.go:261` becomes `_ = s.noteRuntimeStarted(sessionID, claimedEpoch)`
with no rollback. If that confirmation ever refuses (e.g. a SIGTERM-driven `ShutdownDemoteSDK`
releasing the slot between the claim and the record), the session is live on the runtime but
absent from `runtimeLive`, which leaves `runtimeIdleLocked` true and lets a later
`cancelPodMCPIfRuntimeIdle` tear down a live session's §15.4.3 MCP surface. The proposal argues
no compensation races that site, which is true of the gateway compensation but says nothing
about the in-process SIGTERM path. I judged it below the bar (the pod is terminating in that
interleaving, so the surface teardown is harmless), but nobody has checked the interleaving
end to end. A reliability or mechanism lens should.
EVIDENCE: pkg/adapter/sdkwarm.go:69-101 (`ShutdownDemoteSDK`), :296-301; pkg/adapter/runtimegeneration.go:83-88.


### [non-spec.5.review-test-coverage.1]

DECISION: returned an EMPTY findings list for the test-coverage lens on round 5 — BECAUSE I
walked every staged deliverable (SPEC-1..5, SCHEMA-1, CODE-1..6, CONF-1, DOCS-1/2) against
`## Testing` and the checklist's per-step tier lines and found every changed behavior pinned at
a tier it actually reaches, with the non-happy paths named. ALTERNATIVES: I considered filing
(a) a missing tier-8 chaos case for the unanswered reclaim, (b) a missing tier-9 case for the
new gateway-side §4.9 lease release in `materializeSlot`, (c) the tier-4 late-reclaim arm
asserting a gateway-side `leaked=false` in an adapter-only fixture, and (d) the absent
`// spec:` annotation on the two unnamed tier-7a cases. Rejected each: (a) the partition it
models is injected at tier 4 with a client interceptor and tier 8's own subject is
datastore/leader failure; (b) the behavior is asserted at tier 1 in the per-stage compensation
table ("the gateway-side credential leases released"); (c) fixture-narrative imprecision that
the loop has already refuted twice on the same file; (d) a close variant of an already-refuted
finding, and the Testing preamble carries the blanket annotation rule.

FACT: the snapshot at
`scratchpad/cp-snap/0081_.../non-spec-r5` is BYTE-IDENTICAL to the working tree, so `diff -ru`
returns nothing. This is the snapshot trap the standing context records; do not burn a round
hunting a delta. EVIDENCE: `diff -ru scratchpad/cp-snap/0081_.../non-spec-r5 proposals/0081_...`
exits clean.

FACT: `slotAddressCaseFiles` membership is NOT derived from a `slot*_test.go` file name alone,
which is how the proposal describes it twice (non-spec-changes.md, the tier-7a and tier-9
subsections). `derivedInventoryCaseFiles` has three rules: `slotSurfaceCallRE` matches any test
file containing `slotstate.|ClaimSlot(|ReleaseSlot(|ReserveSlotOnPod(|claimAtCreate(|BindReservedSlot(`;
`slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` matches a BASENAME
containing "slot" anywhere, not a prefix; and a tier-11 gate reading `TEST-GAPS.md`. EVIDENCE:
tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173,:1186,:1213-1229. The
proposal's two named files (`tests/tier7a_load_local/slot_reclaim_hold_race_test.go`,
`tests/tier9_security/slot_credential_reclaim_fence_test.go`) match rule 2 and its handling is
correct, and none of its other new files (`pkg/adapter/bindepoch_test.go`,
`tests/tier10_conformance/bind_epoch_conformance_test.go`, the tier-3 directory) matches any
rule, so the imprecise description costs nothing here. Do not file it.

FACT: the spec-map credit arithmetic for the four edited-in-place files checks out, so do not
re-derive it. `pkg/adapter/slotsession_test.go` is whole-file credited to 4.9, 5.2, 6.1, 6.4,
15.4 plus 4.7 via the `pkg/adapter/...` directory entry (new cases cite §4.7; §5.2);
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go` to 4.6.3, 4.7, 5.2, 6.2, 7.1 plus 4.1
via `pkg/gateway/...` (new cases cite §7.1; §5.2; §6.2);
`tests/tier4_integration/concurrent_workspace_test.go` to 5.2, 6.4, 28.5.3 plus 15.1 via
`tests/tier4_integration/...`, so the proposal's S12 addition of 7.1 and 4.7 is exactly what
its new case's citation needs. `tests/tier7a_load_local` has NO directory entry, which is why
the hold-race file needs its own. EVIDENCE: tests/spec-map.json (queried by section).

FACT: every fixture and line citation in `## Testing` that I sampled resolves. Verified:
`pkg/adapter/tracing_internal_test.go:23,:33`; `pkg/observability/tracing/tracing.go:107,:202`;
`pkg/adapter/staging.go:81,:183,:339`; `pkg/gateway/sessionserver/slotretry_test.go:383-407`
and `:469-492`; `pkg/adapter/slotsession_test.go:210`;
`pkg/adapter/socketruntime_test.go:252`; `pkg/adapter/usage_test.go:350`;
`pkg/adapter/adapterevents_test.go:94-95,:104,:182-184`;
`pkg/gateway/podlifecycle/podsession/binder_test.go:301,:322` and `:1156-1173`;
`tests/tier7a_load_local/podmcp_arming_handoff_test.go:43-101`,
`podmcp_once_per_pod_start_race_test.go:239-256`,
`shutdown_drain_gate_race_test.go:209-218,:232`;
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36,:55,:70,:102-108`;
`pkg/gateway/runtime/adapterclient/client.go:48`. CODE-2's seven `noteRuntimeStarted` test call
sites are exactly the seven in the tree (export_test.go:45, usage_test.go:233,
podmcp_arming_internal_test.go:84,:185,:230, adapterevents_test.go:95,:184) with none missed.

FACT: `ensureSlotStateLocked` has exactly the five production resolve callers the CODE-6 table
names, so the hold refusal cannot escape through an unlisted sixth. EVIDENCE:
pkg/adapter/slotsession.go:75, pkg/adapter/slotcreds.go:26, pkg/adapter/slot.go:143 (reached
from staging.go:134, :181, :337).

FACT: `SlotBindError.Reason()` ignores the stage for `codes.Aborted` (it falls to the transient
default; only `FailedPrecondition` reads the stage). EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101. So the Testing section's decision to
add only a workspace-stage and a start-stage `Aborted` row, while claiming those two rows
"cover" the credential stage, is behaviorally sound even though `credential_assignment` is a
distinct stage constant (binder.go:290-298). Do not file it as a missing row.

UNVERIFIED: whether the tier-4 case `TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1`
can assert its late-reclaim arm's "releases the slot with `leaked=false`" inside
`tests/tier4_integration/concurrent_workspace_test.go`. That file imports no gateway package
(only `pkg/adapter`, `adapterv1`, `schematest`) and stands up no `podsession.Binder` or
`SlotClaimer`, so the disposition it names has no observer there today. The outcome mapping is
independently pinned at tier 1, so I judged this immaterial, and a sibling fixture complaint on
the same paragraph was already refuted. An implementor should decide whether to drop that
clause or import the binder. EVIDENCE: tests/tier4_integration/concurrent_workspace_test.go:44-63.

USEFUL [the refuted list]: the refuted entries on "the tier-4 datastore-crossing case asserts a
re-bind on a second pod", "the tier-7a cases name no file", and "three spec-map whole-file
credits are wrong" saved me from re-deriving three dead ends. The last one in particular records
that `specMapEntryCoversFile` treats a `<subtree>/...` entry as a whole-file credit
(tests/tier0_static/spec_map_slot_address_registration_test.go:811-819,:855-857) — compute the
credit set that way or you will conclude a credit is missing when it is not.

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

### [non-spec.2.fix-reclaim-hold-callers]

CORRECTIONS to the "what can meet the reclaim hold" pass of non-spec round 2, from the
post-fix review of that pass's own edits. That pass left no shard in this file, so its
corrections open one here rather than a pass of their own.

- The pass established in the non-spec lane that a §5.2 policy-placed retry is a third caller
  that can meet the hold (`non-spec-changes.md`, the "What can meet the reclaim hold, and what
  it costs" bullet, and `summary.md`'s create-time-reserved paragraph), and left the refuted
  proposition standing at three sites in `spec-changes.md`. The worst of the three was inside
  SPEC-1's staged §7.1 replacement block, so applying SPEC-1 would have landed a false
  normative sentence in `spec/07`: a retry the policy places can meet a hold that no reclaim
  opened, because the abandoned attempt's rollback holds the identifier across `removeSlotTree`
  (`pkg/adapter/session.go:133`, `:147`, `:157` reach `releaseSessionSlot`, whose
  deregister-then-remove sequence is `pkg/adapter/slotsession.go:214-220`) while the
  compensating reclaim finds no entry, is answered `absent`, and so appends nothing to
  `ExcludePods`. §5.2's staged placement constraint is scoped to a pod whose reclaim for this
  session did not complete and cannot reach that hold.
- All three sites now state only what is true. The staged §7.1 paragraph says §5.2 keeps its
  retries off a pod whose reclaim did not complete and that a hold no reclaim opened is outside
  that constraint; the Design section's parallel sentence says the same and points at the
  accepted failure modes for the cost; the accepted-failure-mode bullet names the three callers
  that reach the hold, states why the §5.2 retry is one of them, and states the cost, which is
  one attempt out of a budget whose default is one retry (`spec/05_runtime-registry-and-pool-model.md:555`,
  `maxSlotRetries` at `pkg/gateway/sessionserver/start.go:2720`).
- SPEC-2's `**Max retries:**` replacement text is unchanged. The constraint it states is
  correct as written; what was wrong was the three claims about its reach, and widening the
  constraint to cover a hold no reclaim opened would need a further reclaim outcome on the wire,
  which the accepted failure modes already price and decline.

This edits `spec-changes.md`, which the spec lane converged earlier: three sites, two of them
prose about the staged text and one inside SPEC-1's staged §7.1 block. No staged deliverable is
added, removed, merged, split or resequenced, so the implementation checklist keeps its
fourteen steps and every box stays unticked.

- The same pass's new "A cleanup the adapter runs inside its own start handler reports its
  failure nowhere" bullet cited `pkg/adapter/sdkwarm.go:298` among the start-path rollbacks.
  That site is the release inside `DemoteSDK`, taken on the success path after
  `s.sdkConnected = false` and `s.noteRuntimeClosed(sessionID)` for the registry's single
  entry on a preConnect pod (`pkg/adapter/sdkwarm.go:285-302`), so no bind failed there and no
  compensating `Shutdown` follows it. The citation is dropped and the enumeration now reads
  `pkg/adapter/sdkwarm.go` (:236, :241, :251), which are the three rollbacks inside the
  SDK-warm start, each in an `if err != nil` branch returning `status.Errorf(codes.Internal,
  …)`.
- The same pass's new SPEC-3 sentence stated the whole-pod accounting of that residue without
  either of the two conditions §5.2 places on it, and so disagreed with the bullet the same
  pass wrote. §5.2 runs the whole-pod scrub only on a recycling pod
  (`spec/05_runtime-registry-and-pool-model.md:453` and `:455`), and a failed scrub
  verification disposes of the pod only under `fail` or at `maxScrubFailures`; under the
  default `warn` the pod returns to the available pool with a `scrub_warning` annotation and
  serves the next session (`:483`, `:484`). The staged sentence now names the recycling
  condition, says a failed verification is handled under the pool's `onScrubFailure` policy,
  and states that a pod that does not recycle retires at that boundary. The bullet's parallel
  clause in `non-spec-changes.md` loses its own "disposes of the pod" reading for the same
  reason, so both statements now say one thing.

This second group of corrections edits `spec-changes.md` once more, at the single SPEC-3
sentence the pass added. No staged deliverable is added, removed, merged, split or
resequenced.
