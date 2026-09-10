# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 6).** Pass 6 read the whole ledger for this window: the two
`non-spec-recheck-2.2` fix shards, the `2.2`, `2.3` and `2.4` lens rounds, the two `f5` firings, both
`non-spec` lens rounds, the trailing index-and-checklist reconciliation block, and the
`non-spec-recheck.1.fix-G1` correction list. Lifted: forty-four new Settled facts, twenty-six new
Traps (twenty of them nearly-filed or tried-and-withdrawn MISTAKEs kept with the reasoning that
killed each), nine new Open items and two new Deferred entries. Applied four CORRECTS against the
standing context: no shipped test drives `resumeOnPod` directly, so the tier-1 resume case is an
internal-package build rather than a fixture reuse; the concurrent-resume Open is bounded by §4.6.1
orphan GC rather than by the §5.2 replacement trigger; problem-statement's "0078 widens this
exposure" premise was withdrawn; and the upload-free `stageWorkspace` Trap carried a clause that
Settled #65 contradicts and the tree refutes. Retired as closed: the deadline-margin Open (the
`budget/2` split closes it), the `spec/18` Open (no edit needed), the fourth-reason-value Open
(`codes.Aborted` lands on the existing retryable fallback), and the disputed
`spec-changes.md:98-102` Deferred. Deleted one false Trap clause. Did NOT reach 401 lines: this
section is 599. This window produced one design reversal with a whole mechanism behind it (the
`deadlineMs: 0` to `budget/2` graceful-window split, whose soundness rests on four separate facts
about `resolveShutdownGrace`, the `terminate` frame schema, and which `RuntimeProcess.Close`
implementations read a deadline at all), plus two filed findings and a fixture panic an implementor
will hit. None of those survives compression, so nothing was dropped to reach the number.
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

## Ledger

### [non-spec-recheck-2.2.fix-G1.1]

DECISION: CODE-4's compensating `Shutdown` now splits its two bounds by SHRINKING the adapter-side grace, leaving the gateway's give-up bound at exactly `slotCleanupBudget`. The staged body hoists `budget := slotCleanupBudget(req.CleanupTimeoutSeconds, req.MaxConcurrentSessions)`, uses it for the `context.WithTimeout(context.WithoutCancel(ctx), budget)`, and passes `budget/2` as the fourth argument. BECAUSE with `deadlineMs: 0` the adapter's SIGTERM pivot and the gateway's give-up instant are literally the same instant, so the acknowledged branch is unreachable wherever a runtime honours the plumbed deadline and a completed reclaim is recorded `Leaked`. ALTERNATIVES: growing the RPC bound to twice the §5.2 figure (mirrors the shipped 20s/10s pair, but doubles a worst case that already degenerates to the whole `cleanupTimeoutSeconds`, up to 60s, on an exclusive pool reached through `Binder.Resume`, inside a path that holds the client's request); a named `slotCleanupGrace(budget)` helper or a package constant for the margin (one relation belongs in one place, and a bare constant is a hard-coded non-spec default with no override); changing `slotCleanupBudget` to return `(budget, grace)` (equivalent and acceptable, rejected only because it churns a signature already stated in the staging and in "Files touched" for no behavioural gain — if a later round takes it, take it INSTEAD of the inline `budget/2`, never both); leaving `deadlineMs` at zero and widening the RPC bound alone (cannot work, because with zero the adapter derives its grace FROM the RPC context's deadline, so both numbers move together).

FACT: `resolveShutdownGrace` PREFERS the inbound RPC context's remaining deadline over the runtime-configured grace and over the package default; it is not a fallback of last resort. EVIDENCE: pkg/adapter/mcpruntime.go:311-324, with `contextWithGraceDeadline(ctx, 0)` returning the parent unchanged at pkg/adapter/session.go:327-332.

FACT: the `terminate` frame's `deadlineMs` is a REQUIRED field with `minimum: 100`, and `Shutdown` passes `req.GetDeadlineMs()` straight into `drainViaLifecycle`, so a zero deadline emits a frame missing a required field. `budget/2` is at least 2500ms because the budget carries a five-second floor, so this call site becomes schema-correct as a side effect. EVIDENCE: schemas/runtime-ops-events.schema.json:180-183; pkg/adapter/session.go:260; pkg/adapter/runtimeops.go:65,:486-490.

WATCHOUT: do NOT justify this split with `SocketRuntimeProcess.Close`'s SIGTERM wait. In the shipped sidecar wiring `SpawnPath` is never set, so `cmd == nil` and `Close` returns `p.listener.Close()` with no wait at all. An earlier round filed and withdrew exactly that argument; the standing context carries it as a nearly-filed MISTAKE. Justify with `MCPRuntime` (the §5.1 `type: mcp` runtime, which does read the plumbed deadline), the `terminate` frame's required `deadlineMs`, and the §11.4 precedent. EVIDENCE: `grep -rn "SpawnPath" cmd/ pkg/ --include=*.go` outside tests returns only the field declaration and its own doc at pkg/adapter/socketruntime.go:38,:62-65,:172,:188, so nothing in production assigns it; review-log standing context, Traps, "MISTAKE nearly filed, five performance dresses".

WATCHOUT: do NOT cite `InProcessRuntime.Close` as evidence for any deadline claim. Its signature is `Close(_ context.Context, sessionID string)`, so it discards every deadline and no value of `deadlineMs` changes its behaviour. The same holds for `executor.SubprocessExecutor.Close`, which ignores ctx and hard-codes a ten-second pivot. EVIDENCE: pkg/adapter/embedded.go:188; pkg/gateway/session/executor/subprocess.go:341-367.

DEFERRED [pkg/gateway/runtime/adapterclient/client.go]: the `Client.Shutdown` doc comment at :797 says "A zero deadline lets the adapter apply its default grace period". That is false. When the caller's ctx carries a deadline, `resolveShutdownGrace` takes the ctx's remaining time in preference to both the runtime-configured grace and the package default, so a zero deadline yields the caller's own remaining budget rather than the adapter's default. `adapterclient` is not a target of this proposal and the correction cannot land here. What is true: a zero deadline makes the adapter's SIGTERM pivot equal to whatever the inbound RPC context has left, and only a context with no deadline falls through to the configured grace and then the default.

FACT: `slotCleanupBudget`'s signature and its listing under "Files touched on application" were checked and are unchanged by this fix; the staged budget helper still takes `(cleanupTimeoutSeconds int, maxConcurrentSessions int32)`. Its doc comment now says the gateway reuses §5.2's figure as its own give-up bound and pins half of it as the adapter's graceful window.

FACT: the staged spec text was checked and needs no edit for this. Both §7.1 sites say only that the specification states no deadline for the reclaim and that §5.2's per-slot cleanup timeout is "the figure the gateway reuses". That sentence stays true after the split, because the gateway still reuses the figure. EVIDENCE: spec-changes.md:68-69, :265-266.

FACT: summary open decision 9's clause "the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's teardown" was checked and survives the split unedited, because the adapter's post-`Close` teardown is still unbounded: `noteRuntimeClosed`, `removeSlotTree`'s `os.RemoveAll`, `cancelPodMCPIfRuntimeIdle` and `reportSessionScrub` all run after `Runtime.Close` returns with nothing bounding them. EVIDENCE: pkg/adapter/session.go:263-280.

USEFUL [standing context, Open, "The reclaim's deadline has no margin over the adapter's own cleanup budget"]: that OPEN is exactly this finding, and it named the mechanism and the consequence before the finding was filed. It can be retired as closed by this fix. Its sibling OPEN, "Nothing at any tier pins that the compensating `Shutdown` stays inside its budget", is NOT closed: the tier-1 per-stage bullet now pins the two values' relation, and nothing pins the wall-clock wait.

DECISION: the §11.4 precedent is named in the staged Go doc comment by the constant names `userTerminateRPCTimeout` and `userTerminateDeadline` rather than by a `cmd/lenny-gateway/user_revocation.go:47-55` file:line. BECAUSE a line citation inside a shipped Go comment drifts for the same reason `code-best-practices.md` and channel-naming N8 bar spec line numbers in `// spec:` citations, and the symbol names are greppable and stable. The file:line evidence stays in this log shard. ALTERNATIVES: the design shard placed the parenthetical inside the comment; the substance is unchanged and only the citation form differs.


### [non-spec-recheck-2.2.fix-design-G1.1]

DECISION: fix the `deadlineMs: 0` compensation by SHRINKING the adapter grace, not by growing the
gateway's RPC bound. `slotCleanupBudget` keeps its signature and stays the gateway's give-up bound
on `rctx`; the compensation passes `budget / 2` as `cl.Shutdown`'s fourth argument. — BECAUSE the
gateway's worst case does not grow (standing-context OPEN already records the exclusive-resume
degeneration to the full 60s `cleanupTimeoutSeconds`; doubling it to 120s on a bind-failure path
that holds the client's HTTP request is worse than the defect), no new constant is minted (a
fraction of an existing operator-tunable, so `code-best-practices.md`'s bar on a hard-coded
non-spec constant with no override is not tripped), and §4.7 explicitly lets a caller pin a
shorter grace — the §11.4 fan-out pins 10s regardless of pool config. The 2:1 ratio is the shipped
one: `userTerminateRPCTimeout` 20s over `userTerminateDeadline` 10s. — ALTERNATIVES: (a) RPC bound
= 2 x budget, `deadlineMs` = the whole §5.2 figure — the most faithful reading of §5.2 (its figure
is the ADAPTER's enforcement number) but rejected on the 120s worst case; (b) a named
`slotCleanupGrace` helper or a second constant — hair, one relation belongs in one place;
(c) `slotCleanupBudget` returning `(budget, grace)` — equivalent and acceptable, but do not do it
AND the inline `/2`; (d) harmonising the two shipped zero-deadline sites
(`slotbinder.go:542`, `binder.go:2043`) in the same edit — out of scope.

FACT: with `deadlineMs: 0` the adapter's SIGTERM/SIGKILL pivot and the gateway's give-up instant
are the SAME instant. `contextWithGraceDeadline(ctx, 0)` returns the inbound RPC context
unchanged, and `resolveShutdownGrace` PREFERS that context's deadline over the runtime's
configured grace and over the package default. So the tree's own claim that "A zero deadline lets
the adapter apply its default grace period" is false for any caller that sets an RPC timeout.
EVIDENCE: pkg/adapter/session.go:327-332; pkg/adapter/mcpruntime.go:311-324;
pkg/gateway/runtime/adapterclient/client.go:797-798.

FACT: the strongest production-reachable argument for the fix is NOT the runtime close at all, it
is the `terminate` frame. `Shutdown` calls `drainViaLifecycle(req.GetDeadlineMs(), ...)`,
`RuntimeOps.Terminate` serialises `DeadlineMs int32 json:"deadlineMs,omitempty"`, and the
`terminate` frame REQUIRES `deadlineMs` with `minimum: 100`. A zero-deadline compensating
`Shutdown` on a Full-level pod therefore emits a schema-invalid frame; `budget / 2` is >= 2500ms
at the 5s floor, so the fix makes this call site schema-correct. EVIDENCE:
pkg/adapter/session.go:261, :299-304; pkg/adapter/runtimeops.go:65, :486-490;
schemas/runtime-ops-events.schema.json:179-184. This partly answers the standing-context
UNVERIFIED on the `terminate` frame's `deadlineMs` FOR THIS CALL SITE ONLY; the two shipped
zero-deadline callers are untouched and the pre-existing item stands.

WATCHOUT: do NOT justify this fix with `SocketRuntimeProcess.Close`'s SIGTERM wait. In the shipped
sidecar wiring `SpawnPath` is never set, so `cmd == nil` and `Close` returns `p.listener.Close()`
with no wait at all. A round already filed and withdrew that dress ("five performance dresses" in
the Traps). The runtime implementations that DO read the ctx deadline are `MCPRuntime` (the §5.1
`type: mcp` runtime, wired only in tests today) and `SocketRuntimeProcess` under the dev-only
`SpawnPath`. EVIDENCE: cmd/lenny-adapter/main.go:349-365; pkg/adapter/socketruntime.go:455-467;
grep for `MCPRuntime{` returns only `pkg/adapter/mcpruntime_test.go`.

MISTAKE: the finding cites `InProcessRuntime.Close` blocking on `<-done` as evidence for the fix.
It is not evidence: `func (r *InProcessRuntime) Close(_ context.Context, ...)` discards the
context, so no value of `deadlineMs` changes its behaviour. Same for
`executor.SubprocessExecutor.Close`, which ignores ctx and hard-codes a 10s pivot (and so can
already overrun a 5s budget, independently of this fix). A fixer that copies the finding's
evidence list into the proposal lands two false claims. EVIDENCE: pkg/adapter/embedded.go:188-203;
pkg/gateway/session/executor/subprocess.go:341-367.

FACT: summary.md:256 (open decision 9) — "the reclaim's §5.2 cleanup budget bounds the gateway's
wait rather than the adapter's teardown" — STAYS TRUE after the fix and needs no edit. The
adapter's post-`Close` work (`noteRuntimeClosed`, `removeSlotTree`'s `os.RemoveAll`,
`cancelPodMCPIfRuntimeIdle`, `reportSessionScrub`) is still bounded by nothing, which is the
sentence's whole point. Do not re-litigate it. EVIDENCE: pkg/adapter/session.go:263-280.

DEFERRED [pkg/gateway/runtime/adapterclient/client.go]: the `Client.Shutdown` doc comment at
:797-798 says "A zero deadline lets the adapter apply its default grace period". That is false:
`resolveShutdownGrace` prefers the inbound context's deadline over both the configured grace and
the package default, so a zero deadline gives the adapter the CALLER's remaining time, and the
adapter's own default applies only when the caller set no deadline at all. The remedy is a
one-sentence doc-comment correction in a file this proposal does not target. Whoever next opens
`adapterclient` should land it.

OPEN: `compensateFailedSlotBind`'s signature is still staged as `req SlotBindRequest` while
`Binder.Resume` holds a `ResumeRequest` (a standing OPEN). Whoever converts it to scalars must
carry BOTH `CleanupTimeoutSeconds` and `MaxConcurrentSessions` through, because the budget and the
derived grace both read them. Landing the scalar conversion and this grace fix in two independent
edits is how one of them loses an argument.


### [non-spec-recheck-2.2.review-applicability.1]

FACT: The whole `Aborted` delta verifies clean against the tree. Every citation in CODE-2's new
"choice of code is deliberate" paragraph resolves exactly: `SlotBindError.Reason()`'s
FailedPrecondition→policy_rejection arm is at slotfailure.go:91-99, `NonRetryable()` at :41-48, the
transient default at :100-101, `slotFailureSessionStart` minted at slotbinder.go:322-324 and defined
at binder.go:293, `classifySlotBindFailure` at start.go:2761 with its pass-through at :2766-2769,
`writeSlotFailed` (422 SLOT_FAILED, retryable:false, no Retry-After) at start.go:312-320,
`Server.Checkpoint`'s Aborted at checkpoint.go:115, oplock.go:37/:82, and the FailedPrecondition
sites at session.go:104, resume.go:33/:42, slotsession.go:279-280, coordination.go:133/:281.
`grep -rn "codes.Aborted" pkg/gateway cmd --include=*.go` returns NOTHING outside pkg/adapter, so no
gateway consumer branches on the code. `adapterclient.Client.StartSession` returns the raw gRPC
error (client.go:142-143), so `slotErrCode`'s errors.As finds the status. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101; pkg/gateway/runtime/adapterclient/client.go:142

FACT: `spanErr` and `tracing.CategorizeError`/`tracing.CategoryTransient` really are in scope at
session.go:163 (the var is declared at session.go:83 and drained by the deferred RecordError at
:84-87), and `Server.Resume` opens no span at all, so the asymmetry the delta states is real.
EVIDENCE: pkg/adapter/session.go:82-87; pkg/adapter/resume.go:25-35; pkg/observability/tracing/tracing.go:88,:218

FACT: The two shipped classifier tables the new tier-1 rows extend are exactly where the proposal
says. `TestSlotBindErrorReason_spec_5_2` is at slotretry_test.go:383 with the
`{"session_start", codes.PermissionDenied, SlotReasonPolicyRejection}` row at :387, and its loop
carries an extra invariant assertion `c.want.NonRetryable() == (c.want == SlotReasonTransient)` that
an `{..., codes.Aborted, SlotReasonTransient}` row satisfies.
`TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` is at :468 and passes `req(...)` BY
VALUE, so it is genuinely untouched by CODE-5's pointer threading.
EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:383-407,:468-492

WATCHOUT: The `slotBinder` interface at pkg/gateway/sessionserver/start.go:2724-2728 declares
`ReleaseSlotReservation(ctx, sandboxName, slotID string) error`, and CODE-4 changes it. There are
exactly TWO implementers in tests — `fakeSlotBinder` (slotretry_test.go:52) and
`concurrentSlotBinder` (slotretry_load_test.go:33) — and both stop compiling. CODE-5's rewritten
fixture paragraph says the opposite ("The `slotBinder` interface is unchanged … both test fakes
compile as they stand"). The fixer corrected the by-value/by-pointer half of that sentence and left
the interface half wrong. `fakeSlotBinder.released` is `[][2]string{pod, slotID}`, so it also cannot
carry the `leaked=true` the tier-1 accounting case asserts.
EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:33-56; slotretry_load_test.go:33-37

FACT: The tier-11 gate's anchors are all exact. `generalSlotEdges` is the four-entry slice at
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36, the positive loop over
the either-concurrency block is at :55 and the negative loop over the concurrent-occupancy block at
:70, and `requireAllContain` over the reference page's per-slot section is at :102-108. In spec/06
§6.2 the concurrent-occupancy block (:146-148) precedes the either-concurrency block (:150-155),
which is what makes the `scopedBlock := s62[Index(scopedHeader):Index(generalHeader)]` slice work,
so SPEC-4's insertion after `receiving_uploads ──→ running` (:152-153) lands on the correct side.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-70; spec/06_warm-pod-model.md:146-155

FACT: `tests/tier4_integration/concurrent_workspace_test.go` really wires no CH-RUNTIMEOPS — it sets
WorkspaceBase/SessionsRoot/ArtifactsRoot/CredentialsDir and `srv.Runtime`, and never `srv.Lifecycle`
(:108-126). So the delta's reason for dropping the §15.4.2 assertion from the tier-4 case is sound,
and `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` at
tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304 does drive two STARTED co-tenants
from one rendezvous and assert exactly one terminate frame, exercising both arms of `boundRemains`.
EVIDENCE: tests/tier4_integration/concurrent_workspace_test.go:108-126; tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304

USEFUL [Traps: `bound ⊋ started`]: the predicate-nesting entry in Standing context saved re-deriving
CODE-1's three gates from scratch; the shipped `Shutdown` at session.go:237-291 matches it exactly
(deregisterSlotLocked returns (st, removed, boundRemains) at slotsession.go:174).

MISTAKE (nearly filed, withdrawn): I almost filed S10's `Depends on: S9` as omitting S2 and S3,
whose SPEC-2 `**Max retries:**` constraint and SPEC-3 leak-accounting clause CODE-5 implements. It
is not a finding under the lens's own class-6 wording, which fires only when a code step implements
a statement staged by a LATER step. S2 and S3 precede S9, which S10 names, so the transitive check
the implement-proposal skill describes holds. Do not re-file.

MISTAKE (nearly filed, withdrawn): the tier-1 case "The rollback's status code classifies transient"
tests CODE-2's behaviour but sits under "### Gateway tests for CODE-4 and CODE-5, tier 1", i.e. it
is built at S9/S10 rather than S8. Not a defect: the rows assert only shipped classifier behaviour
(Reason() already maps Aborted→transient today), so they pass at any point, and CODE-2's own changed
answer is asserted at S8 by the tier-1 "Start-versus-reclaim rollback, deterministic form" case.


### [non-spec-recheck-2.2.review-feasibility.1]

FACT: The whole delta since the r6 snapshot is (a) CODE-2's rollback status code
FailedPrecondition -> Aborted plus the new justification paragraph, (b) the CODE-5 pointer-threading
rewrite, (c) the tier-4 §15.4.2 decline rewrite, (d) two new slotretry_test.go table rows, (e) summary:
F-5.2.33 attribution, the new lagging-rollback accepted-failure bullet, the 0078 row rewrite, the new
0080 §1.7 row, and the deletion of the reserved-branch out-of-scope row. `diff -ru` against
non-spec-recheck-2-r2 shows ZERO change in the two staged files (only the review log moved), so use the
r6 snapshot for the delta. — EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-r6/

FACT: Every citation in the new `Aborted` paragraph checks out, and I opened all of them:
slotfailure.go:41-48 (NonRetryable), :91-99 (FailedPrecondition->policy_rejection outside
slotFailureWorkspacePrep), :100-101 (transient default); slotbinder.go:322 mints
slotFailureSessionStart; binder.go:293 declares it; start.go:2761 classifySlotBindFailure by value;
start.go:313 writeSlotFailed = 422 SLOT_FAILED retryable:false no Retry-After; start.go:70-86 +
:270-275 STARTING_FAILED = retryable 503 with Retry-After; adapter Aborted sites checkpoint.go:115 /
oplock.go:37,:82; FailedPrecondition sites session.go:104, resume.go:33,:42, slotsession.go:280,
coordination.go:133,:281; Server.Resume opens no span (resume.go:25 has no tracing call, unlike
session.go:82-83). Do not re-verify these. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:91

FACT: No gRPC interceptor, service config, or retry policy anywhere in pkg/gateway/runtime/adapterclient
or cmd/lenny-adapter treats a status code specially, and `codes.Aborted` appears in no non-test file
outside pkg/adapter/{oplock,checkpoint}.go. The Aborted switch therefore cannot collide with a transport
or classifier the proposal did not name. — EVIDENCE: grep -rn "codes.Aborted" pkg/ cmd/ --include=*.go

WATCHOUT: `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648) branches ONLY on typed
errors and sentinels; it returns false for any bare gRPC status whatever its code. So the resume-path
rollback's `Aborted` is inert there and a raced Resume demotes the row to `failed`, exactly as the
adjacent shipped `codes.Internal` runtime-start failure at resume.go:140 already does. The proposal's
resume bullet says the resume rollback answers Aborted "for the classification reason the StartSession
rollback records above", and that reason (applySlotRetryPolicy + classifySlotBindFailure) runs on the
start path only. I considered filing it and did not: the code choice changes no behaviour on the resume
path and the sentence is defensible as cross-rollback consistency. Do not spend a round on it unless you
can show a resume-path consumer that reads the code. — EVIDENCE: pkg/gateway/sessionserver/start.go:3648-3681

FACT: CODE-1 does newly withhold the §15.4.2 drain frame for a bound-but-unstarted reclaim (today's gate
is `bound := removed && st.sessionID != ""` at session.go:239, used at :243, with drainViaLifecycle at
:259-260). That withheld signal IS asserted, in the tier-1 "Bound but unstarted" case, so the tier-4
paragraph's decline is sound. The shipped tier-7a drain tests all start their sessions through
`startDrainSession` (a full StartSession, shutdown_drain_gate_race_test.go:211-218), so CODE-1 breaks
none of them. — EVIDENCE: tests/tier7a_load_local/shutdown_drain_gate_race_test.go:211

FACT: All the test-fixture citations in the delta resolve: gatedRuntime at
tier7a_load_local/podmcp_arming_handoff_test.go:43-105, the one-rendezvous-two-RPC form at
podmcp_once_per_pod_start_race_test.go:236-257, TestConcurrentShutdownsSendOneDrainSignal_spec_6_4 at
shutdown_drain_gate_race_test.go:232-304, TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2 at
socketruntime_test.go:252 with cleanups at :258 and :316, slotretry_test.go:69 `req`,
slotretry_test.go:383-407 and :468-492, slotretry_load_test.go:84-87, and the tier-11 file's
generalSlotEdges/:55/:70/:102-108. `assignCredentialsSlot` is production code
(pkg/adapter/slotcreds.go:23), not a missing fixture. — EVIDENCE: pkg/adapter/slotcreds.go:23

FACT: The summary's new external attributions are accurate. BUILD-GAPS F-5.2.33 is open at
BUILD-GAPS.md:4132 and its suggested resolution splits into (a) drop p.listener.Close() and (b) name the
successor-runtime actor; proposal 0078's own scope paragraph claims (a) and hands (b) to 0079.
socketruntime.go:467 is `return p.listener.Close()`; the sibling early return is :441-446; the listener
is bound once at :156-161; Interrupt at :398-417 leaves `connected` true with `conn` non-nil. The
hold-state allowlist really does admit only the fence, NegotiateVersion, AdapterEvents and the two
grpc.health methods (holdstate.go:52-57), so the "compensation is refused in hold state" bullet holds.
— EVIDENCE: BUILD-GAPS.md:4137

MISTAKE: The CODE-5 "Where it is read" bullet still asserts "The `slotBinder` interface is unchanged …
so every implementing type and both test fakes compile as they stand" (non-spec-changes.md:649-651).
CODE-4's own call-site table five hundred lines earlier changes `ReleaseSlotReservation` on that very
interface (:498), the shipped declaration is
`ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string) error`
(pkg/gateway/sessionserver/start.go:2726), and both fakes implement the three-arg form
(slotretry_test.go:52, slotretry_load_test.go:33). The round that rewrote the neighbouring `req`-helper
half of this sentence fixed the pointer clause and left the interface clause standing. Filed.

DEFERRED [none]: nothing derived here needs a file this loop may not edit.


### [non-spec-recheck-2.2.review-reliability.1]

FACT: The whole `Aborted` delta verifies clean, citation by citation. `Reason()`'s `default:` →
`SlotReasonTransient` (podsession/slotfailure.go:100-101), `NonRetryable()` false for it (:41-48),
`FailedPrecondition` outside `workspace_prep` → `policy_rejection` (:91-99), `slotFailureSessionStart`
minted at slotbinder.go:322-324 with the constant at binder.go:293, `classifySlotBindFailure` passing a
transient through at start.go:2761-2779 and `applySlotRetryPolicy` retrying at :2873-2879, `spanErr`
in scope at session.go:83 with the record site at :163, `Server.Resume` opening no span
(resume.go:25-35), `codes.Aborted` used only on the checkpoint op lock (checkpoint.go:115,
oplock.go:36-40,:82), `FailedPrecondition` at session.go:104, resume.go:33/:42, slotsession.go:274-283,
coordination.go:133/:281. Also verified the two shipped test tables the new coupling rows go into
(slotretry_test.go:383-407, :468-492) and the by-value `req` helper at :69 with the load-test call site
at :84-87. Nobody needs to re-open any of these. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:100

FACT: The tier-4 delta's replacement justification is accurate. `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4`
really is at tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232, really does end two bound
co-tenants from one rendezvous, and really does assert exactly one `terminate` frame, so both arms of
`boundRemains` are pinned there. EVIDENCE: tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304

FACT: The 0078 impact-row rewrite's citations all resolve: socketruntime.go:467 is
`return p.listener.Close()`, the listener is bound once at :156-161, `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
is at socketruntime_test.go:252 with the `defer sp.Close` cleanups at :258 and :316, and the tier-4
cleanup is at tests/tier4_integration/concurrent_workspace_test.go:126. EVIDENCE: pkg/adapter/socketruntime.go:467

FINDING (filed): the compensating `Shutdown` passes `deadlineMs: 0` while its RPC deadline is
`slotCleanupBudget`, so the adapter derives its SIGTERM→SIGKILL pivot from the SAME instant the gateway
gives up: `contextWithGraceDeadline(ctx, 0)` returns the inbound ctx (session.go:327-332) and
`resolveShutdownGrace` then returns the whole remaining budget (mcpruntime.go:312-324). A runtime that
uses its grace is therefore ALWAYS reported unacknowledged → `Leaked=true` → occupancy held for the life
of the pod + `RecordLeak` + pod excluded. The tree's own counter-example is
cmd/lenny-gateway/user_revocation.go:50-55, whose comment says the RPC timeout "exceeds
userTerminateDeadline so the gateway observes the pod's graceful exit before giving up on the call".
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824; pkg/adapter/socketruntime.go:452-467

FINDING (filed): CODE-5 says "The `slotBinder` interface is unchanged … both test fakes compile as they
stand" (non-spec-changes.md:649-651) while CODE-4's own call-site table changes that interface
(non-spec-changes.md:498). `fakeSlotBinder.ReleaseSlotReservation` (slotretry_test.go:52) and
`concurrentSlotBinder.ReleaseSlotReservation` (slotretry_load_test.go:33) both carry the three-parameter
form and stop satisfying the interface. Same shape as the accepted `req`-pointer finding an earlier round
confirmed. EVIDENCE: pkg/gateway/sessionserver/slotretry_test.go:52

MISTAKE (mine, withdrawn before filing): "the rollback runs `Runtime.Close` on a cancelled inbound ctx,
so the teardown is skipped". Closed by the standing Settled entry and re-verified: all three `Close`
implementations read ctx only through `resolveShutdownGrace`, which falls back when `time.Until(dl) <= 0`,
so a cancelled ctx makes the close harsher, never absent. Do not rebuild it.

MISTAKE (mine, withdrawn before filing): "a successful retry's later release erases pod A's leak record,
because `slotstate.Registry` is keyed on slotID and slotID == sessionID". `MarkReleased` has NO production
caller (`grep -rn MarkReleased --include=*.go pkg cmd` returns the definition only), and `MarkLeaked` /
`ForgetPod` have exactly one production caller each, both in `applySlotRetryPolicy`. The residual defect
is real but cosmetic: a SECOND leak for the same session on a different pod keeps the first record's
`pod` field (registry.go:105-114), so the second pod's `lenny_adapter_leaked_slots` gauge is published
with the first pod's count. The drain threshold is unaffected, because `slothealth.Tracker.RecordLeak` is
keyed by pod. Pre-existing and metric-only; refutable. EVIDENCE: pkg/sandbox/slotstate/registry.go:99-115

UNVERIFIED closed: the standing Open "does §5.2's client-error contract want a fourth reason value for a
reclaimed-slot race" is answered no. `Aborted` passes through `classifySlotBindFailure` unclassified
(start.go:2766-2772) and lands on the existing retryable `STARTING_FAILED` / `SESSION_CREATION_FAILED`
fallback in `writePodClaimError`, so no new client-visible code and no `docs/reference/error-catalog.md`
row is owed. EVIDENCE: pkg/gateway/sessionserver/start.go:2761-2779

WATCHOUT: the fix for the deadline finding must also touch the tier-1 assertion at
non-spec-changes.md:819, which currently pins `deadlineMs` zero on the per-stage compensation table.
Changing the call without changing that line leaves the staging self-contradictory.


### [non-spec-recheck-2.3.review-reliability.1]

DECISION: returned an EMPTY findings list — BECAUSE the round's only delta (the
`deadlineMs = budget/2` split in `compensateFailedSlotBind`, non-spec-changes.md:406-455,
:842, summary.md:81-86) was traced end to end through the adapter and holds; nothing else in
the staging failed a reliability trace under crash, restart, cancelled-context or
store-failover. ALTERNATIVES: (a) filing the §5.2 "minimum 5s enforced at runtime by the
adapter" versus the 2.5s graceful window the split pins at the floor — dropped, because
`deadline_ms` bounds only `Runtime.Close`'s SIGTERM/SIGKILL pivot while the whole cleanup
still has the full budget from the RPC deadline, so nothing is lost and no residue survives;
(b) filing the open-decision-9 staleness below — dropped as framing of an open decision,
which this lens may not file on, and because the conclusion survives.

FACT: the delta's mechanism is CORRECT and here is the full trace, so nobody re-derives it.
`adapterclient.Client.Shutdown(ctx, sessionID, reason, deadline time.Duration)` converts with
`int32(deadline.Milliseconds())`, so `budget/2` is a legal fourth argument and not a
nanosecond/millisecond confusion. The adapter feeds `req.GetDeadlineMs()` to BOTH
`drainViaLifecycle` (session.go:260) and `contextWithGraceDeadline(ctx, deadlineMs)`
(session.go:263). `contextWithGraceDeadline` returns the PARENT unchanged for a non-positive
grace and `WithTimeout(parent, grace)` otherwise (session.go:327-332), and every
`RuntimeProcess.Close` reads the derived ctx through `resolveShutdownGrace`, which prefers
the ctx deadline's REMAINING time over the configured grace and only then falls back
(mcpruntime.go:308-324). So the close is bounded at ~budget/2 while the RPC gives up at
budget: the margin the split exists for is real.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824; pkg/adapter/session.go:260,:263,:327-332;
pkg/adapter/mcpruntime.go:294-305,:308-324; pkg/adapter/socketruntime.go:452-467.

FACT: every citation the delta adds resolves. `pkg/adapter/session.go:260` IS
`s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())`.
`schemas/runtime-ops-events.schema.json:180-183` IS the `deadlineMs` `minimum: 100` property
plus the `required` list, and the 5s budget floor puts the sent value at 2500ms, above it.
The §11.4 analogy is exact: `userTerminateRPCTimeout = 20s` bounds the call and the shorter
`userTerminateDeadline = 10s` is the fourth argument.
EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55,:128-129; schemas/runtime-ops-events.schema.json:174-185.

FACT: the shipped ordinary release still sends deadline 0 (`result.Adapter.Shutdown(ctx,
result.SessionID, "", 0)`), so after the delta the compensating reclaim is the ONLY gateway
caller on the slot path that pins a graceful window, and a lens comparing the two paths will
see 2.5s versus the 10s `defaultSocketShutdownGrace` fallback. That asymmetry is deliberate
and argued at non-spec-changes.md:448-455.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542.

DEFERRED [proposals/0081_.../0081_....summary.md, open decision 9 at :258-260]: it reads
"the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's
teardown". After this round's delta the call pins `deadline_ms = budget/2`, which DOES bound
one part of the adapter's teardown, the runtime close's grace. What is true instead: the
budget bounds the gateway's wait and the runtime close's grace, and bounds neither the
handler's deregistration nor `removeSlotTree`, which are the destructive steps that race a
retry, so decision 9's conclusion (nothing separates the two attempts) is unchanged. Not
filed: framing of an open decision is outside this lens, and the imprecision runs in the
conservative direction. A fixer touching that sentence should repair it.

WATCHOUT: `slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32)` divides
by `maxConcurrentSessions`, and the delta adds a SECOND consumer of its result inside the same
call. The three `podsession.ResumeRequest{...}` literals in
`pkg/gateway/podlifecycle/podsession/binder_test.go` (:492, :533, :1650) leave
`MaxConcurrentSessions` at zero. None of them reaches the compensation today (two resumes
succeed and the third fails at `ErrNoIdlePod`, before the adapter RPC), so no shipped test
panics, but a fixture that drives `cl.Resume` to failure with that field unset would divide
by zero in the gateway request path. `resumeRequest(n)` in
`resume_slot_reservation_test.go:51` always passes 1, 2 or 4, so the shipped failing-resume
case (`TestResumeReleasesItsReservationWhenTheAdapterResumeFails_spec_5_2`) is safe.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:492,:533,:1650;
pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go:51-57,:74,:106,:145,:193,:223,:246,:281.

USEFUL [Standing context / Settled 215]: "the denominator is >= 1 at every reachable call
site ... A test fake leaving the field zero would panic" is what pointed me at the fixture
literals above rather than at the production path; it saved a full re-derivation and the
WATCHOUT above is its concrete site list.

USEFUL [Standing context / Settled 130]: the "diff -rq over the whole snapshot directory
first, take the newest snapshot that differs" rule was exactly right again. This round's
named snapshot `non-spec-recheck-2-r3` is byte-identical to the live proposal; the real delta
is `non-spec-recheck-2-r2-prefix` -> live and it is two hunks in
`.non-spec-changes.md` plus one in `.summary.md`.


### [non-spec-recheck-2.4.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE the round-3/round-4 delta (CODE-2's
`codes.Aborted` rollback plus its `spanErr` line, `compensateFailedSlotBind`'s
`budget`/`budget/2` split, the tier-4 §15.4.2 deletion, the new classification-row test
bullet, and the summary's 0078/0080-§1.7/lagging-release rows) is applicable end to end and
introduces no forward reference, no unresolvable anchor, no lost relocation leg and no
checklist defect — ALTERNATIVES: I built and dropped four candidates, each recorded below.

FACT: the whole staged-anchor sweep still runs clean at commit f2a397b5 and the spec-changes
file did not move in this window. A 10-line python pass over the 28 fenced blocks of
`.spec-changes.md` against `spec/*.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24
and 0 for every other block, reproducing Settled #228 exactly. EVIDENCE:
proposals/0081_.../0081_....spec-changes.md (fenced blocks); spec/04,05,06,07.

FACT: `adapterclient.Client.Shutdown`'s fourth parameter is `deadline time.Duration`, not a
millisecond integer, so the staged `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed",
budget/2)` compiles as written and the int32 conversion happens inside the client
(`int32(deadline.Milliseconds())`). A reviewer expecting a numeric-literal mismatch from the
`0` → `budget/2` change will find none. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:817.

FACT: the `budget/2` split is mechanically sound on the adapter side and makes the RPC deadline
strictly outlast the close grace. `drainViaLifecycle` does not block (it is a single
`Lifecycle.Terminate` send, errors logged), and `contextWithGraceDeadline(ctx, deadlineMs)`
derives from the inbound RPC context, so the handler's worst case is min(budget, budget/2) plus
the tree removal. EVIDENCE: pkg/adapter/session.go:260,:263,:299-307,:327-332.

FACT: every citation the delta minted resolves. `slotfailure.go:41-48` (NonRetryable),
`:91-99` (the FailedPrecondition → policy_rejection arm), `:100-101` (the transient default);
`slotbinder.go:322-324` and `binder.go:293` (slotFailureSessionStart); `checkpoint.go:115` and
`oplock.go:36-40,:82` (the adapter's only `codes.Aborted` sites — grep over `pkg/adapter/*.go`
returns no third); `slotretry_test.go:383-407` and `:468-492` (both tables exist and already
carry a `{"session_start", codes.PermissionDenied, …}` row); `resume.go:25-35` (no span),
`:33,:42` (the two FailedPrecondition preconditions); `session.go:133,:147,:157` and
`resume.go:69,:73,:89,:107,:126,:134,:141` (the lagging-release row's branches);
`runtime-ops-events.schema.json:180-183` (deadlineMs minimum 100, required);
`user_revocation.go:45,:50,:55,:129` (the 10s/20s relation the doc comment cites);
socketruntime_test.go:252,:258 and socketruntime.go:441-446,:467 and
tier4_integration/concurrent_workspace_test.go:126 (the rewritten 0078 impacts row).

MISTAKE (nearly filed, four dresses, all killed on the evidence):
(1) "`budget/2` is a `time.Duration` passed where a millisecond int is expected" — killed by
    client.go:807's `time.Duration` parameter.
(2) "the tier-1 assertion `strictly less than the RPC deadline` needs a fixture seam the
    proposal does not stage" — killed because the RPC deadline is a pure function of the
    case's own pool configuration (`slotCleanupBudget`), so the assertion is arithmetic on
    the recorded `DeadlineMs` and needs no `ctx.Deadline()` read in the fake.
(3) "the new classification-row test is CODE-2's (S8) but is filed under the CODE-4/CODE-5
    gateway section, so no step carries it" — killed: S9 and S10 both list tier 1 and both
    name that section, the rows assert the SHIPPED classifier so they are green at any point
    in the sequence, and tests are not deliverables the checklist has to name one-to-one.
(4) "open decision 9's `the reclaim's §5.2 cleanup budget bounds the gateway's wait rather
    than the adapter's teardown` is falsified by the split" — killed: `deadline_ms` bounds
    only the `Runtime.Close` grace; the destructive `removeSlotTree` the decision is about is
    still unbounded and the handler still performs no context-expiry check (Settled #112), so
    the sentence's operative half survives.

FACT: the checklist re-derives clean against the current text. Ten steps, ten deliverables,
one lane each (spec ×4 leading, docs ×1, code ×5), no deliverable named twice, no unstaged
deliverable named, every `Depends on` naming an earlier existing step, no checked box, and no
code step consuming a spec statement a later step stages (S7→S1,S3; S8→S2; S6→S4; S9→S2,S3;
S10→S9→S9's own chain). Settled #178 still holds word for word; the delta added no step, no
deliverable and no tier obligation. Do not re-derive it unless a fix round adds a deliverable.

WATCHOUT: `SlotBindRequest.CleanupTimeoutSeconds` is `int` while `MaxConcurrentSessions` is
`int32`, so the staged `slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions
int32)` signature is right but its body needs an explicit `int(maxConcurrentSessions)` for the
division. That is implementation detail rather than underspecification; do not file it.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:39,:108.

USEFUL [Standing context #70, #178, #180, #228]: the anchor sweep, the checklist derivation
and the code-target inventory were all still accurate, which is what let this pass spend its
budget on the delta instead of re-deriving them. Three of my four withdrawn candidates died on
Settled entries (#112, #182, #215) rather than on fresh reading.


### [non-spec-recheck-2.4.review-citations.1]

DECISION: returned an empty findings list for the citation lens — BECAUSE I opened and line-checked every concrete citation the r6→now delta added, plus a broad sweep of the pre-existing ones, and all resolved with no meaning-changing drift — ALTERNATIVES: filing the one soft spot I found (the resume rollback's `codes.Aborted` justified "for the classification reason the `StartSession` rollback records above", when no §5.2 category classifier runs on the resume path at all), rejected because it is inert prose rationale: `Reason()` is reached only through `applySlotRetryPolicy` and `classifySlotBindFailure`, neither of which the resume path calls, and `writePodClaimError`'s default arm gives the identical 503 + Retry-After under either code.

FACT: the whole delta's new citation set was verified, so a future round need not re-open them — EVIDENCE: `pkg/gateway/podlifecycle/podsession/slotfailure.go:91-99` (FailedPrecondition outside workspace_prep → policy_rejection), `:41-48` (NonRetryable), `:100-101` (transient default), `:74` (Unwrap); `slotbinder.go:322-324` + `binder.go:293` (slotFailureSessionStart), `slotbinder.go:210-224` (BindReservedSlot), `:528-545`, `:542-543`; `start.go:2140`, `:2172` (claimAtCreate arm), `:2602` (bindConcurrentSlot reserved arm), `:2606-2609` (runWithQueue closure), `:2720` (maxSlotRetries=1), `:2761`, `:2766-2779`, `:2809`, `:4041`, `:3648-3682`; `slotretry_test.go:69`, `:383-407`, `:468-492`; `slotretry_load_test.go:84-87`; `queue.go:103-107,:143-146,:205`; `pkg/adapter/checkpoint.go:115`, `oplock.go:36-40,:82`, `session.go:104,:133,:147,:156,:157,:163,:243,:260,:309-321`, `resume.go:25-35,:33,:42,:50,:69,:73,:89,:107,:126,:134,:140,:141,:144`, `slotsession.go:87,:214-220,:274-283,:338,:375`, `coordination.go:133,:281`, `runtimegeneration.go:26-49,:58-69,:83-88`, `slot.go:140-148,:210-212`, `slotcreds.go:34`, `holdstate.go:89-99,:177-190,:251`, `socketruntime.go:156-161,:184,:398-417,:427-429,:441-446,:467`; `cmd/lenny-gateway/user_revocation.go:45,:128-129`; `schemas/runtime-ops-events.schema.json:180-183`; `spec/28_communication-channels.md:1082`.

FACT: every "text to replace, verbatim" anchor in the spec-changes file still matches `spec/` byte-for-byte (12 anchors machine-checked), and SPEC-4's two insertion anchors (`spec/06_warm-pod-model.md:152` and the `**`reserved` hold semantics.**` paragraph at `:158`) both exist — EVIDENCE: spec/04_system-components.md:157, spec/06_warm-pod-model.md:150-158.

FACT: `deadlineMs = budget/2` is wire-legal on every pool. `Client.Shutdown`'s fourth argument is a `time.Duration` converted with `.Milliseconds()` into int32 (`pkg/gateway/runtime/adapterclient/client.go:807,:817`); the adapter forwards it verbatim into the terminate frame (`pkg/adapter/session.go:260`) and into the runtime-close grace (`:263` → `contextWithGraceDeadline`, `:327-332`); the frame schema fixes `minimum: 100` and the 5s budget floor puts half at 2500ms — EVIDENCE: schemas/runtime-ops-events.schema.json:180-183.

WATCHOUT: the "no §15.4.2 assertion is made here" paragraph in the tier-4 case reads as if the drain behaviour were unchanged. It is not: CODE-1 moves the drain gate from `bound && !boundRemains` to `started && !boundRemains`, so a bound-but-unstarted reclaim on a solo pod newly sends no terminate frame. That change IS asserted, but at tier 1 in the "Bound but unstarted" case ("**no `terminate` frame on CH-RUNTIMEOPS**, which is new"), not at tier 4 — EVIDENCE: non-spec-changes.md tier-1 "Bound but unstarted" bullet; pkg/adapter/session.go:243,259-261.

USEFUL [earlier rounds]: the refuted list saved real time — several shapes I re-derived independently (the `slotBinder` interface gloss, the `SlotBindRequest`-vs-`ResumeRequest` helper signature, the tier-4 re-bind fixture) were already adjudicated as immaterial, and re-reading them stopped me refiling them.


### [non-spec-recheck-2.4.review-client-surface.1]

DECISION: Returned an empty findings list for the client-facing-surface lens — BECAUSE the whole client-surface inventory (REST/OpenAPI, MCP/A2A, wire proto, JSONL + runtime-ops-events schemas, adapter manifest, runtime/client SDKs, CRDs, client-visible enums and error codes, docs/api + docs/client-guide + docs/runtime-author-guide + docs/reference) came back consistent after the round-3 `budget/2` delta — ALTERNATIVES: I built and discarded four candidates, each recorded below with the reason.

FACT: The round-3 delta is entirely the graceful-window split on the compensating `Shutdown`: `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", budget/2)` with the RPC context still bounded by `budget`. Every wire claim it makes checks out. `Client.Shutdown`'s fourth parameter is `deadline time.Duration` and is converted with `int32(deadline.Milliseconds())`, so `budget/2` is type-correct and cannot overflow int32 at any admissible `cleanupTimeoutSeconds`. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807, :813-818.

FACT: The §11.4 precedent the delta cites is exact, including the rationale sentence. `userTerminateRPCTimeout = 20s` "exceeds userTerminateDeadline so the gateway observes the pod's graceful exit before giving up on the call"; `userTerminateDeadline = 10s`. Same 2:1 relation, same stated reason. EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55, :128-129.

FACT: The `terminate` frame minimum the delta relies on is real and the arithmetic clears it. `deadlineMs` has `"minimum": 100` and is `required`; the 5s budget floor puts `budget/2` at 2500ms or above on every pool configuration. The adapter passes `req.GetDeadlineMs()` verbatim into the frame and separately into the close grace via `contextWithGraceDeadline`. EVIDENCE: schemas/runtime-ops-events.schema.json:180-183; pkg/adapter/session.go:260, :262-265, :299-306.

FACT: The per-slot sub-state vocabulary this proposal extends has exactly three parallel representations and all three are staged. `spec/06_warm-pod-model.md:150-155` (SPEC-4), `pkg/sandbox/slotstate/slotstate.go:99-100` (CODE-3), `docs/reference/state-machines.md:234-237` (DOCS-1), plus the tier-11 reconciliation pair. There is no fourth: `grep -rn "slot_cleanup" docs/ schemas/ sdks/ charts/ pkg/embedded/` returns only state-machines.md:236-237,:251 and a proto comment, and no SVG in docs/assets/diagrams/ draws the per-slot machine. The per-slot states are also barred from external API responses by spec/15_external-api-surface.md:672, so no OpenAPI/SDK/CRD mirror exists to update.

FACT: `ShutdownResponse.exited_cleanly` has NO doc comment in the proto and NO mention anywhere in `spec/`, `docs/`, or `sdks/` (`grep -rn "exited_cleanly\|exitedCleanly\|ExitedCleanly" spec/ docs/ sdks/` is empty). So CODE-1's change of its meaning to `closeErr == nil && (live || treeErr == nil)` falsifies no parallel representation. This kills the most obvious client-surface candidate before it starts. EVIDENCE: schemas/lenny-adapter.proto:1665-1668.

WATCHOUT: `schemas/lenny-adapter.proto:436-437` and `:451-453` both say the per-slot cleanup outcome is reported "on every session release", which SPEC-3 + CODE-1 narrow. I did NOT file it: the proto is barred by programme rule S-2, the pre-change tree already withholds the report for a registered-but-unbound entry, and the material skeptic already refuted the identical wording at `docs/reference/adapter-contract.md:84` on the ground that "at each session release" "stay[s] true under the proposal's framing". Do not re-file this under a proto heading; it is the same sentence in a file this proposal may not open.

WATCHOUT: `docs/api/internal.md:145-160` documents a `StopSession` / `StopSessionRequest` / `StopSessionResponse` RPC with a `clean_exit` field. No such RPC or message exists in `schemas/lenny-adapter.proto`; the real one is `Shutdown` / `ShutdownRequest` / `ShutdownResponse` with `exited_cleanly`. That divergence is fully pre-existing and this proposal neither widens nor narrows it, so it is not a 0081 edit site. A future corpus sweep should own it separately.

MISTAKE (mine, caught before filing): I nearly filed that CODE-2's `Resume` rollback answering `codes.Aborted` demotes the session terminally, because `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3681) maps neither `Aborted` nor `FailedPrecondition` to transient, unlike the §5.2 slot classifier the proposal's rationale cites. It is not a defect: the rollback only fires when the gateway's own call has already failed and issued the compensation, so the code it returns goes to a caller that has already abandoned the RPC. The code choice is inert on the resume path, and both candidate codes give the same classification there. Cost: about fifteen minutes.

UNVERIFIED: whether `docs/reference/state-machines.md:237`'s `slot_cleanup → released` trigger text ("Slot workspace removed, processes killed, slot released") should follow SPEC-3's widening of §5.2's action list to name the per-slot credential directory and the §4.9 timer cancellation. I judged it incompleteness rather than falsity and did not file. A docs-content lens could reach a different answer.


### [non-spec-recheck-2.4.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the docs-alignment lens on the r6→now delta — BECAUSE every docs surface the delta touches resolves to already-refuted, already-recorded-as-pre-existing, or a standing trap — ALTERNATIVES: I built and dropped four candidates, each recorded below with the ground that killed it, so a later docs lens does not rebuild them.

FACT: the r6→now delta is exactly six things, and NONE of them reaches a `docs/` surface. (1) CODE-2's rollback status code `FailedPrecondition` → `Aborted` plus `spanErr = tracing.CategorizeError(..., CategoryTransient)` on `StartSession` only; (2) the compensating `Shutdown`'s fourth argument goes from `0` to `budget/2` while the RPC deadline stays `budget`; (3) two new table rows in `slotretry_test.go`; (4) the tier-4 §15.4.2 assertion deleted and replaced by a pointer at a shipped tier-7a test; (5) a new out-of-scope summary entry on the lagging pre-`Runtime.Start` release branch; (6) rewritten 0078 row, new 0080 §1.7 row, deleted reserved-branch conformance-gap row. — EVIDENCE: diff of scratchpad/cp-snap/0081/non-spec-recheck-r6 against the proposal dir.

FACT: EVERY citation in the delta verifies. slotfailure.go:91-99 (FailedPrecondition→policy_rejection), :41-48 (NonRetryable), :100-101 (transient default); slotbinder.go:322-324 + binder.go:293 (`slotFailureSessionStart`); checkpoint.go:115 and oplock.go:36-40,:82 (`codes.Aborted` precedent); session.go:104 / resume.go:33,:42 / slotsession.go:274-283 / coordination.go:133,:281 (FailedPrecondition already overloaded); start.go:2766-2779 and :2873-2879; session.go:260 (deadline_ms → frame); runtime-ops-events.schema.json:180-183 (deadlineMs required, minimum 100); user_revocation.go:50,:55,:128-129 (RPC timeout 20s outlasting the 10s graceful window — the stated §11.4 precedent is real); slotretry_test.go:69 (`req` by value), :383-407, :468-492; start.go:2140/:2172 (claimAtCreate) and :2594/:2602/:2606-2609 (bindConcurrentSlot, by-value `slotReq` parameter); `bindSlotWithRetry` genuinely has no test caller. I re-derived all of these; do not re-derive.

FACT: the budget/2 split's stated relation actually holds in the tree, which is worth knowing because the obvious objection is that it does not. `contextWithGraceDeadline` derives closeCtx from the inbound rctx (session.go:322-329), `SocketRuntimeProcess.Close` reads that deadline through `resolveShutdownGrace(ctx, 0, defaultSocketShutdownGrace)` and pivots to `cmd.Process.Kill()` AT the grace deadline rather than grace+10s, then reaps (socketruntime.go:457-466). So a 5s budget gives a 2.5s close that returns inside the 5s RPC deadline. The "gateway observes the real outcome" claim is sound. — EVIDENCE: pkg/adapter/socketruntime.go:435-467, :473; pkg/adapter/session.go:322-336.

FACT: `drainReason` normalises every unrecognised `ShutdownRequest.reason` to `session_complete` (pkg/adapter/session.go:309-321), so `"slot_bind_failed"` never reaches the wire and neither spec/28:1082 nor schemas/runtime-ops-events.schema.json:181 nor docs/api/internal.md:432 is falsified. Verified against pkg/adapter/drain_test.go:58-64, which already pins the mapping for `""`, `"drain"` and `"garbage"`.

WATCHOUT: `docs/reference/adapter-contract.md:210-216` is a `reason`-value table with per-value MEANINGS (`"session_complete"` = "Session has completed normally"), and after this change a raced compensating reclaim sends `session_complete` for a session whose bind failed. It looks like a fresh docs finding and it is not: the table already lists `"drain"`, which is not in the four-value enum at all, the example at :205 uses it, and the shipped §11.4 revoke already sends `"USER_REVOKED"` down the same normaliser to `session_complete`. Pre-existing enumeration/semantics gap, same class as the `WARM_POOL_EXHAUSTED` MISTAKE at standing-context :376. — EVIDENCE: docs/reference/adapter-contract.md:205,:210-216; cmd/lenny-gateway/user_revocation.go:45,:129.

WATCHOUT: `docs/api/internal.md:490-500` is a gRPC status-code table for the adapter surface and it carries no `ABORTED` row, so CODE-2's new code looks unmirrored. It is not a finding: the shipped adapter already returns `codes.Aborted` from `Server.Checkpoint` (checkpoint.go:115) and `codes.InvalidArgument` from `StartSession` (session.go:96), neither of which the table lists. The table is already incomplete in two ways before this proposal. — EVIDENCE: docs/api/internal.md:492-500.

WATCHOUT: `docs/reference/adapter-contract.md:208` says "sends SIGTERM, then SIGKILL after 10 seconds", while `SocketRuntimeProcess.Close` SIGKILLs AT the grace deadline with no SIGTERM at all. Real divergence, entirely pre-existing, and the new `budget/2` window does not create or widen it. Do not file it on this proposal.

FACT: the metric surfaces are clean and I re-derived why. CODE-4/CODE-5 add NO `recordSlotFailure` call, so `lenny_slot_failure_total`'s docs row ("Per-slot failures on session-mode pods with `maxConcurrentSessions > 1`", docs/reference/metrics.md:166) stays true even though the §7.3 re-attach path now reaches the health ledger. `lenny_slot_pod_replacement_total`'s row (:167, "Pod replacements triggered by slot failures") is generic and survives the three new caller sites. `lenny_adapter_leaked_slots` is absent from docs/reference/metrics.md, but SPEC-3 is its THIRD spec mention, not its first — spec/05:545 and spec/06:160 already name it — so the docs gap is pre-existing. — EVIDENCE: non-spec-changes.md CODE-5 helper body; spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:160.

FACT: the tier-4 §15.4.2 deletion is covered, and the replacement pointer is accurate. `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` really does sit at tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304, really does start both co-tenants through `startDrainSession` and really does assert `peer.count("terminate") != 1`, pinning both arms of `boundRemains`. Separately, the withheld-drain behaviour CODE-1 introduces IS asserted: the tier-1 "Bound but unstarted" case lists "**no `terminate` frame on CH-RUNTIMEOPS**" with the no-other-bound-entry precondition spelled out. So the deletion left no coverage hole.

FACT: DOCS-1 is the only docs site the per-slot change has, and I re-confirmed the sweep independently rather than trusting the log. `receiving_uploads` as a PER-SLOT state exists at docs/reference/state-machines.md:234-235 alone; the hits in docs/runtime-author-guide/lifecycle.md:27,:39,:52 and in docs/assets/diagrams/pod-warm-path.svg:26 / sdk-warm-path.svg:29 are the POD-level machine (`claimed → receiving_uploads → finalizing_workspace`), which SPEC-4 does not touch. DOCS-1's staged row text matches SPEC-4's fence annotation word for word, and the staged tier-11 substring `` `receiving_uploads` | `slot_cleanup` `` matches the staged row verbatim.

USEFUL [standing context :461]: the `docs/reference/adapter-contract.md:75` DEFERRED, with its explicit "eleven separate lenses have now filed the :75 site independently" and the four secondary sites marked NOT edit sites. That entry plus the orchestrator's refuted list is what stopped me spending the round rebuilding the twelfth instance. The material skeptic refuted it; it is out of bounds.

USEFUL [standing context :218, :219, :367]: the case-sensitive first-match property of `lineContaining`. I verified it at tests/tier11_docs/backup_status_enum_test.go:48-55 and confirmed SPEC-3's append writes "whole-pod replacement trigger stated below" in lower case while the gate anchors on capitalised "Whole-pod replacement trigger", so none of the four §5.2 gates is redirected. Do not re-derive.

MISTAKE: none found in the delta. The two design reversals this window shipped (the `Aborted` status code and the `budget/2` graceful window) are both correct fixes to items on the already-found list, and both are grounded on citations that hold.

UNVERIFIED: whether S5, which the checklist labels a `docs` step, is permitted to write `tests/tier11_docs/*.go` under a docs-scoped write lease. The proposal deliberately puts the tier-11 edits in DOCS-1's step (non-spec-changes.md, DOCS-1 and the tier-11 subsection) and the checklist line says "Tiers 0, 11". If the implement-proposal lease is lane-scoped by path rather than by tier, S5 cannot land its own gate. Someone who knows the lease mechanics should confirm; it is not a docs-alignment defect either way.

OPEN: `SLOT_FAILED` is a client-visible 422 code emitted at pkg/gateway/sessionserver/start.go:313 and it appears in NO file under `spec/` or `docs/` — `grep -rn SLOT_FAILED spec/ docs/` returns nothing, so docs/reference/error-catalog.md and docs/client-guide/error-handling.md both lack a row for it. Entirely pre-existing and this proposal mints no new code, so it is not a finding here, but it is a real documentation gap that some proposal should own.


### [non-spec-recheck-2.4.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier this round's delta adds or changes (`codes.Aborted` on the two rollbacks, the `budget/2` graceful window, `ExcludePods`, `SlotBindError.Leaked`, `slotCleanupBudget`, `compensateFailedSlotBind`, `materializeSlotStages`, `accountSlotFailure`, `runtimeHoldsLocked`) is in-process Go with no spec/, docs/, schemas/ or charts/ mirror, and every carrier that could have become wrong was grepped and is either untouched or already-refuted — ALTERNATIVES: filed nothing on `schemas/lenny-adapter.proto`'s `Shutdown` RPC comment or `docs/api/internal.md`'s gRPC-code table, both close variants of already-refuted findings and both pre-existingly stale.

FACT: `codes.Aborted` is ALREADY returned by the shipped adapter, at `pkg/adapter/checkpoint.go:115` for a coalesced or busy op lock (`pkg/adapter/oplock.go:37,:82`). So CODE-2's choice mints no new code for the adapter surface, and `docs/api/internal.md:489-499`'s "gRPC status codes / When used" table — which lists OK, UNAVAILABLE, NOT_FOUND, ALREADY_EXISTS, DEADLINE_EXCEEDED, UNIMPLEMENTED, INTERNAL, FAILED_PRECONDITION, RESOURCE_EXHAUSTED and NOT Aborted — is already incomplete before this proposal. Do not file it as a new edit site. — EVIDENCE: pkg/adapter/checkpoint.go:115; docs/api/internal.md:499

FACT: every delta citation was opened and is exact. `slotfailure.go:91-99` (the FailedPrecondition→policy_rejection arm), `:41-48` (NonRetryable), `:100-101` (transient default); `slotbinder.go:322-324` and `binder.go:293` (slotFailureSessionStart); `start.go:2761` (classifySlotBindFailure by value), `:2172` (claimAtCreate's arm), `:2140` (its slotReq), `:2602` (bindConcurrentSlot's reserved arm), `:2606-2609` (runWithQueue closure), `:2720`, `:2809`, `:2834`, `:3246`; `slotretry_test.go:69` (`req` helper), `:383-407`, `:468-492`; `slotretry_load_test.go:84-87`; `session.go:104,:163,:260,:309-321`; `resume.go:25-35,:33,:42,:50,:140,:144`; `slotsession.go:214-220` (removal at `:217`), `:274-283`; `coordination.go:133,:281`; `slot.go:140-148,:210-212`; `slotcreds.go:34`; `schemas/runtime-ops-events.schema.json:180-183` (deadlineMs required, minimum 100) and `:181` (reason enum); `cmd/lenny-gateway/user_revocation.go:45,:50,:55,:129`; `socketruntime.go:441-446,:467,:156-161`; `socketruntime_test.go:252,:258,:316`; `tests/tier4_integration/concurrent_workspace_test.go:126`; `tests/tier7a_load_local/shutdown_drain_gate_race_test.go:209-218,:232-304`.

FACT: the ordinary end-of-session `Binder.ReleaseSlot` sends `Shutdown(ctx, sessionID, "", 0)` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:542`), so the shipped tree already emits a `terminate` frame with `deadlineMs: 0` against a schema whose minimum is 100. That is pre-existing and NOT something CODE-4's `budget/2` creates or worsens; the compensating call is the only `Shutdown` in the tree that will satisfy the minimum. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542; pkg/adapter/session.go:260; schemas/runtime-ops-events.schema.json:180

FACT: CODE-1's gate move (`bound` → `started`/`removed`/`live`) breaks no shipped test OUTSIDE `pkg/adapter`, and I verified this rather than trusting the proposal's scope paragraph, which is scoped to adapter tests only. Every non-adapter caller of the adapter's `Shutdown` drives a full `StartSession` first or holds no entry at all: `tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go`'s `startAndShutdownSlot` (:133-151), `tests/tier2_component/warmlayout/warm_layout_test.go:137-165`, `tests/tier4_integration/mcp_runtime_lifecycle_test.go:138,:157`, `tests/tier10_conformance/recycle_scrub_conformance_test.go:174-186` (started) and `:269-283` (deliberately no StartSession, so `removed=false` and the response is still clean), `tests/tier2_component/slotrelease/revoke_double_teardown_test.go:223` (bound slot, then the idempotent second `Shutdown`), and the two tier-7a drain tests. — EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:133-151; tests/tier10_conformance/recycle_scrub_conformance_test.go:269-283

FACT: `grep` over `tests/`, `scripts/`, `pkg/`, `cmd/` for the exact sentences SPEC-1 and SPEC-3 replace ("closes that session's runtime", "releases the session's slot", "removes the slot's workspace directory", "per-session teardown when the adapter holds a bound entry", "Graceful end-of-session teardown", "field's presence standing in for a scope") returns nothing. No tier-0 or tier-11 gate keys on the replaced spec text, so the only gate the spec edits reach is the tier-11 per-slot reconciliation pair DOCS-1 already extends.

FACT: the tier-11 mechanism DOCS-1 relies on works as the proposal describes. `generalSlotEdges` (`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37`) feeds a positive loop over the either-concurrency block and a negative loop over the concurrent-occupancy block; `scopedBlock` is sliced `scopedHeader..generalHeader`, and spec/06 orders the scoped block (:146-148) BEFORE the general block (:150-156), so the slice is non-empty. The staged three-line edge annotation still puts the literal `receiving_uploads ──→ slot_cleanup` on one line, so `strings.Contains` matches. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:55,:70; spec/06_warm-pod-model.md:146-156

FACT: no docs/ page mirrors §5.2's slot-cleanup ACTION LIST or §5.2's `**Max retries:**` placement sentence. Greps for "process group", "slot's workspace directory", "fresh slot", "slot retry", "fully saturated" over `docs/` return nothing. The §5.2 edits therefore owe no docs deliverable beyond DOCS-1. `lenny_slot_failure_total` and `lenny_slot_pod_replacement_total` are both inventoried as `maxConcurrentSessions > 1`-scoped (`spec/16_observability.md:14-15`, `docs/reference/metrics.md:166-167`), and all three of CODE-5's accounting callers are gated to concurrent pools, so neither inventory row becomes wrong.

FACT: both `bindConcurrentSlot` call sites sit inside `if match.MaxConcurrentSessions > 1` (`pkg/gateway/sessionserver/start.go:2351-2353` and the two-step `/start` arm at `:2479`), which is what makes the summary's new 0080 §1.7 impact row true when it says no staged code produces a `leaked` disposition on an exclusive pod.

FACT: the summary's new BUILD-GAPS claim checks out. `F-5.2.33` is OPEN (unticked) at `BUILD-GAPS.md:4132`, its suggested resolution splits into (a) the listener close and (b) the successor-runtime actor (`:4137`), and proposal `0078_fix_keep-the-pod-listener-across-a-session-teardown.md` names itself part (a) and 0079 part (b) in its own Scope block. 0078's deliverable ids CODE-1, CODE-2, TEST-1..TEST-7 and DOCS-1 all exist (`:87,:93,:96,:99,:102,:779-787`).

WATCHOUT: the review log's `### Deferred` entry against `spec-changes.md:98-102` ("a blob-store outage does not retire healthy pods") is STALE — that bullet was repaired and now reads "...adds nothing to the pod's persistent leak count. The windowed failure counter still records it, and at `maxConcurrentSessions: 2` a single windowed failure already reaches the §5.2 whole-pod replacement threshold." Do not re-file it and do not re-apply it. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:97-103

WATCHOUT: `slotCleanupBudget`'s staged doc comment gives the formula `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` and states only the `cleanupTimeoutSeconds`-unset case. A literal implementation panics if `maxConcurrentSessions` is 0. It is not reachable in production — `ResumeRequest.MaxConcurrentSessions` is documented as "normalized to a minimum of 1 by the caller" (`pkg/gateway/podlifecycle/podsession/binder.go:641-645`) and `SlotBindRequest` is only built on the `> 1` branch — but a unit-test fixture can pass 0. I judged this below the bar rather than a design defect; an implementor should guard the denominator anyway. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:641-645

UNVERIFIED: the adapter's `Resume` now answers `codes.Aborted` on the CODE-2 rollback, and `isTransientPodClaimError` (`pkg/gateway/sessionserver/start.go:3648-3682`) returns false for a bare `Aborted`, so a resume that loses the race demotes the row to `failed` rather than holding it in `awaiting_client_action`. I did not file it because the pre-CODE-2 `Resume` failures on that path (`codes.Internal` from `Runtime.Start`) already take the same branch, so the code choice adds no new class. A later round wanting to sharpen the resume-side retryability story should start there, not from the slot classifier the new tier-1 rows cover.


### [non-spec-recheck-2.4.review-feasibility.1]

DECISION: returned an EMPTY findings list — BECAUSE every actor the staging names exists
under that name and can perform what it is assigned, and the round's delta (`deadline_ms
= budget/2` on the compensating `Shutdown`, non-spec-changes.md:406-455, :838-843,
summary.md:83-86) was re-traced end to end through the adapter and holds.
ALTERNATIVES: (a) filing that `InProcessRuntime.Close` ignores its context entirely, so the
delta's "the fourth argument is the graceful window the adapter spends on the runtime close"
is false for one of the three shipped `RuntimeProcess` implementations — dropped, because the
split is strictly an improvement there (the pre-delta behaviour is what remains) and no staged
behaviour, test or citation turns on it; (b) filing the `slotCleanupBudget` divide-by-zero on a
`ResumeRequest` with `MaxConcurrentSessions` unset — dropped, the sole production caller
normalises through `maxConcurrentSessions(match.MaxConcurrentSessions)` at
`pkg/gateway/sessionserver/start.go:4029`, and the residual is a test-fixture concern the
r3 shard already recorded.

FACT: `InProcessRuntime.Close(_ context.Context, sessionID string)` DISCARDS its context and
blocks unbounded on `<-done` (`pkg/adapter/embedded.go:188-204`). Only `MCPRuntime.Close` and
`SocketRuntimeProcess.Close` read the plumbed deadline through `resolveShutdownGrace`. Any
future claim that "the adapter's runtime close is bounded by `deadline_ms`" is true for two of
three implementations, not three.
EVIDENCE: pkg/adapter/embedded.go:188-204; pkg/adapter/mcpruntime.go:294-324;
pkg/adapter/socketruntime.go:452-467.

FACT: a grace-expired close is reported as CLEAN, not as an error. Both
`MCPRuntime.Close` and `SocketRuntimeProcess.Close` reach the `time.After(grace)` arm, SIGKILL
the child, and return nil (the socket one returns `p.listener.Close()`). So `closeErr == nil`
and `exited_cleanly` is true even when the runtime had to be killed. That is pre-existing and
this proposal does not change it, but a lens reasoning about "the gateway observes the
reclaim's real outcome" should know the observable is "the RPC answered", not "the runtime
exited gracefully".
EVIDENCE: pkg/adapter/mcpruntime.go:296-305; pkg/adapter/socketruntime.go:456-466.

FACT: the sdkwarm `noteRuntimeStarted` guard is precise and not a hole. `fresh == false` is
returned ONLY when `st.started` was already true (`claimSessionSlotUnderLock` returns
`false,...,nil` on the `idempotentRepeat` arm), so the non-fresh arm's session is already in
`runtimeLive` from its first call. CODE-1's `live` gate therefore withholds no ordinary
session-end `ReportSessionScrub` on an SDK-warm pod. I chased this as a candidate
report-suppression regression and it is closed.
EVIDENCE: pkg/adapter/sdkwarm.go:217,:258-262; pkg/adapter/slotsession.go:79-89.

FACT: `Binder.Resume` newly returning a `*SlotBindError` in the chain reaches no other
consumer that branches on that type. `createClaimNeedsRollback`
(`pkg/gateway/sessionserver/start.go:3211-3223`) IS an `errors.As(err, &slotBindErr)` gate that
decides whether to release a create-time reservation, but its two call sites (`:833`, `:1026`)
take a `startOnPod` error, never a resume one. `writePodClaimError` branches on
`*podsession.SlotFailedError`, a different type. `isTransientPodClaimError` reads through
`Unwrap`. Nothing needed changing.
EVIDENCE: pkg/gateway/sessionserver/start.go:833,:1026,:3211-3223,:3494,:3648-3682.

FACT: the tier-11 `generalSlotEdges` edit lands correctly. `scopedBlock` is
`s62[scopedHeader:generalHeader]` and the concurrent-occupancy block sits ABOVE the general
block in `spec/06_warm-pod-model.md` (:146-148 vs :150-155), so adding
`"receiving_uploads ──→ slot_cleanup"` to the slice satisfies the positive loop and the
negative loop simultaneously once SPEC-4 inserts the edge after :152.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-36,:55,:64-70;
spec/06_warm-pod-model.md:146-155.

FACT: every delta citation resolves. `pkg/adapter/session.go:260` is the
`drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())` call;
`schemas/runtime-ops-events.schema.json:180-183` is the `deadlineMs` `minimum: 100` property
plus the `required` list; `cmd/lenny-gateway/user_revocation.go:45` is
`const userRevokeReason = "USER_REVOKED"` and `:129` is the `Shutdown` call carrying
`userTerminateDeadline` (10s) inside a `userTerminateRPCTimeout` (20s) context, so the 2:1
analogy the delta draws is exact.

WATCHOUT: `contextWithGraceDeadline` returns the PARENT UNCHANGED for a non-positive grace
(`pkg/adapter/session.go:327-332`). That is why the pre-delta `deadline_ms = 0` made the close
grace equal the full remaining RPC budget and the confirmed "can never be reported as cleanly
reclaimed" defect real. Anyone tempted to "simplify" the split back to a single bound
re-opens that defect.
EVIDENCE: pkg/adapter/session.go:263,:327-332; pkg/adapter/mcpruntime.go:308-324.

USEFUL [Standing context / Settled 117]: "The reclaim's deadline is bounded ... §7.1's 'states
no deadline' reads as unbounded only in isolation" is what stopped me filing the §7.1
no-deadline sentence against the delta's two-bound call. Saved a wasted candidate.

USEFUL [Standing context / Settled 111]: "The `Shutdown` handler performs no context-expiry
check" is the fact that makes the delta safe rather than dangerous — the handler runs
destructively to completion regardless of which of the two bounds expires first, so shortening
the inner bound cannot strand the reclaim half-done.



### [non-spec-recheck-2.4.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE the round's whole delta is the `deadlineMs` split in `compensateFailedSlotBind` (`budget/2` as the fourth `cl.Shutdown` argument, plus its two rationale paragraphs, the `slotCleanupBudget` doc comment, the summary budget bullet, and the tier-1 per-stage assertion), and every factual claim it makes re-verified exact against the tree; a full independent sweep of the rest of the staging turned up nothing above the bar that is not already on the refuted or already-fixed list — ALTERNATIVES: filing the open-decision-9 clause "the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's teardown" (rejected: it now under-describes the split, but the instruction bars filing on how an open decision is framed except for a false citation, and the clause's load-bearing half — the unbounded `os.RemoveAll` after the close — is still true); filing "the compensating reclaim's `terminate` frame value has no test" (rejected: the derivation budget>=5s => budget/2>=2500ms>=100 is arithmetic, and the gateway-side value is asserted in the tier-1 per-stage compensation table).

FACT: the delta's four load-bearing citations are all exact. `pkg/adapter/session.go:260` is `s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())`; `pkg/adapter/session.go:263` derives the close context from the same field; `schemas/runtime-ops-events.schema.json:180` is `"deadlineMs": { "type": "integer", "minimum": 100 }` and `:183` is the `required` list; `cmd/lenny-gateway/user_revocation.go:50,:55` are `userTerminateDeadline = 10 * time.Second` and `userTerminateRPCTimeout = 20 * time.Second`, an exact 2:1 that matches `budget` / `budget/2`. EVIDENCE: those files and lines.

FACT: the delta fixes a second, unstated defect nobody named. Before it, `deadlineMs: 0` made `contextWithGraceDeadline(ctx, 0)` return the inbound RPC context unchanged (`pkg/adapter/session.go:327-332`), so `SocketRuntimeProcess.Close`'s `resolveShutdownGrace` sized its pivot at the FULL gateway budget and the RPC deadline expired at the same instant — the misreported-leak defect the round closed. It ALSO meant the compensating reclaim's `terminate` frame carried `deadlineMs: 0`, below the schema's `minimum: 100`. `budget/2 >= 2500ms` closes both. EVIDENCE: pkg/adapter/socketruntime.go:435-467; pkg/adapter/runtimeops.go:486-492; schemas/runtime-ops-events.schema.json:173-184.

FACT: the delta does NOT disturb the standing WATCHOUT on `pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go`. That test drives a failing `Binder.Resume` against a real `adapter.New(...)`; the adapter holds no entry for the session, so `started`, `live` and `treeErr` are all false/nil whatever `deadline_ms` carries, `ExitedCleanly` is still true, `Leaked` is still false, and `releaseResumeSlot` still decrements. The trap's "re-check it if CODE-4's disposition wiring changes" is not triggered: the disposition wiring is unchanged.

WATCHOUT: `slotCleanupBudget`'s BODY is not staged, only its signature and doc comment, and the formula it states divides by `maxConcurrentSessions`. Every reachable caller supplies >= 1 (`ResumeRequest.MaxConcurrentSessions` is doc-commented "normalized to a minimum of 1 by the caller" at binder.go:640-645, and `ClaimSlot` rejects `< 1` outright at slotclaimer.go:393-395), so a divide-by-zero is not reachable today — but an implementor writing the body from the doc comment alone has nothing telling them so. EVIDENCE: non-spec-changes.md:380-388; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:393-395; pkg/gateway/podlifecycle/podsession/binder.go:640-645.

DEFERRED [summary.md:259]: open decision 9's clause "the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's teardown" was written when the compensating `Shutdown` sent `deadlineMs: 0`. After the delta, half the budget IS pinned as the adapter's graceful window and bounds the runtime-close half of the teardown (`contextWithGraceDeadline` -> `resolveShutdownGrace` -> the `cmd.Wait` select in `SocketRuntimeProcess.Close`). What is still unbounded, and what actually carries decision 9's hazard, is `removeSlotTree`'s `os.RemoveAll`, which runs after the close with no context at all. The accurate clause is "the reclaim's slot-release half runs under no deadline at all". Not landed here because the instruction bars a lens from filing on how an open decision is framed. EVIDENCE: non-spec-changes.md:419; pkg/adapter/session.go:263-273; pkg/adapter/socketruntime.go:455-466.

USEFUL [Traps: "Grepping spec prose for a quoted phrase fails SILENTLY when the phrase wraps across a source line"]: saved a false "anchor does not exist" finding on the §7.2 preamble deletion, whose anchor spans a wrap at spec/07:210.

USEFUL [Traps: the §5.2-append-redirects-four-tier-11-gates entry]: I re-swept it against the current append text and it still holds green. The four `requireLine(t, s52, ...)` anchors in the tree are "Whole-pod replacement trigger", "Session count limit", "Uptime limit", "increments `<metric>`" (concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155,:222,:298), plus "The gateway triggers the whole-pod scrub" (recycle_scrub_trigger_consistency_test.go:70,:144), "**Slot (session mode).**" and "A service-mode slot is a different thing" (slot_definition_glossary_reconciliation_test.go:84,:127), and "**Fresh-guest reprovision:**" and "the pod is held for its tenant through the claim's `reserved` state" (vm_restart_reprovision_consistency_test.go:71,:197). None of those substrings occurs in SPEC-3's append, case-sensitively. That is the full list; a future round need not re-derive it.

FACT: `ReleaseSlotReservation` has exactly six call sites in the tree and CODE-4's table covers all six, one-for-one: slotbinder.go:172 (ClaimSlot connect-stage), slotbinder.go:217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), start.go:2727 (the `slotBinder` interface), start.go:2834 (applySlotRetryPolicy), start.go:3246 (rollbackClaim). No seventh site exists. EVIDENCE: `grep -rn ReleaseSlotReservation --include=*.go . | grep -v _test`.


### [non-spec-recheck-2.4.review-kubernetes.1]

DECISION: Returned an empty findings list for the Kubernetes-idiom lens — BECAUSE every K8s-touching surface this proposal opens is either the shipped, §4.6.3-blessed pattern or a pre-existing condition the proposal does not create. ALTERNATIVES: I built and then killed three candidates, each recorded below with the evidence that killed it, so a later agent does not rebuild them.

FACT: `ExcludePods` reaches no Kubernetes object. `SlotClaimer.reserveSlot` persists only `{Pool, SessionID, TenantID}` onto the claim via `CreateClaim` (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:696-697`) and stamps only `req.MaxPodUptimeSeconds` onto the Pod (`:747`). A new `SlotRequest` field is therefore in-process placement state, not persisted CRD state. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:673-747

FACT: `ClaimSlot`'s pass-1 candidate scan reads the per-pod `SandboxClaim` and never `Sandbox.status.phase`; only pass-2 reads the phase (`sb.Status.Phase != string(state.Idle)`). The proposal's claim at non-spec-changes.md:706-708 that a `DrainSandbox` stamp does not keep the immediate retry off a pass-1 pod is exact. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:417-425, :475-477

FACT: the shipped siblings the staged tier-2 cases sit beside carry an explicit §4.6.3 ownership assertion, `assertNotGatewayStatusOwned(t, c, "sbx-old")`, plus a "phase unchanged" check. The staged `ExcludePods` cases do not need it (the skip is a bare `continue`, no write), but an implementor copying those fixtures will see it. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go:648-709

MISTAKE (mine, withdrawn before filing): I nearly filed "the staged `leaked` disposition leaves a phantom `SandboxClaim` in `bound` with occupancy 1 that no level-triggered reconciler reclaims." It is wrong on the spec. §4.6.1's orphan-GC predicate 1 reclaims, by draining, a claim in `bound` whose last binding-state transition is older than `claimOrphanTimeout` (default 5 minutes) and whose pod no active session references. A leaked slot on a pod with no live co-tenant is exactly that claim, so the level-triggered path exists. Where a live co-tenant does reference the pod, the held occupancy is §6.2's stated `leaked` semantics rather than a stuck object. EVIDENCE: spec/04_system-components.md:517 (predicate 1); pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836

MISTAKE (mine, withdrawn): I nearly filed the `DrainSandbox` reached from CODE-5's new `resumeOnPod` caller as a gateway write into controller-owned state. §4.6.3's Gateway ServiceAccount paragraph grants the gateway `get`/`patch` on agent Pods precisely for the `lenny.dev/drain-request` annotation, and `Binder.DrainSandbox`'s own doc comment records that routing (rather than a `Sandbox.status` write) as the §4.6.3-conformant form. A new caller of an already-blessed mechanism is not an idiom violation. EVIDENCE: spec/04_system-components.md:622 (Gateway ServiceAccount RBAC grants); pkg/gateway/podlifecycle/podsession/slotbinder.go:588-603

FACT: every summary line-cite into `slotclaimer.go`'s release disposition is exact as of this run — `:830-836` (the leaked early return), `:845-847` (sibling slots remain, claim stays), `:850-878` (the recycle patch plus armed missing-report timeout), `:881-885` (`DeleteClaim` at occupancy zero on a non-recycling pool). `ReleaseSlotReservation` hard-codes `recycle=false`, so a clean occupancy-zero reservation release always takes the DELETE arm, never the recycle arm, on a recycling pool too. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:487-503; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:818-886

FACT (delta verification, all confirmed): the `codes.Aborted` rewrite's whole citation chain holds. `SlotBindError.Reason()` maps `PermissionDenied`/`FailedPrecondition` outside `workspace_prep` to `policy_rejection` (slotfailure.go:91-99), `NonRetryable()` is true for it (:41-48), the start stage is minted `slotFailureSessionStart` (slotbinder.go:322-324, binder.go:293), and `Aborted` falls to the transient default (slotfailure.go:100-101). `spanErr` exists in `StartSession` (session.go:83) and `Server.Resume` opens no span at all, so the asymmetric treatment is right. The two new test rows land in real tables: `TestSlotBindErrorReason_spec_5_2` at slotretry_test.go:383-407 with the `PermissionDenied` row at :391, and `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` at :468-492. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:41-103; pkg/adapter/session.go:76-84; pkg/adapter/resume.go:25-35

FACT (delta verification): the `budget/2` split's supports hold. `Client.Shutdown`'s fourth parameter is a `time.Duration` converted to `DeadlineMs` (adapterclient/client.go:807-818); the adapter passes `req.GetDeadlineMs()` straight into the terminate frame at session.go:260; the frame schema requires `deadlineMs` with `minimum: 100` at schemas/runtime-ops-events.schema.json:180-183, and the 5s floor puts half at 2500ms. The §11.4 precedent is real and holds the same direction: `userTerminateRPCTimeout` 20s bounds the call, `userTerminateDeadline` 10s is the graceful window sent. `drainViaLifecycle` does not block (it sends one frame and logs), so the adapter's wall clock inside the RPC is `Runtime.Close` bounded by budget/2 plus `removeSlotTree`, comfortably inside the budget. EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55, :128-129; pkg/adapter/session.go:296-306

FACT (delta verification): the CODE-5 pointer-threading correction is accurate on both halves. `req(pool, mcs)` at slotretry_test.go:69 returns by value; the load test's `applySlotRetryPolicy` call is inside the goroutine body at slotretry_load_test.go:84-87, so binding a local and taking its address there does give each goroutine its own request. `classifySlotBindFailure` is at start.go:2761, its create-time-reserved caller is `claimAtCreate` at :2172 (passing the `slotReq` built at :2140) and its reserved-bind caller is `bindConcurrentSlot` at :2602 — the round's re-attribution off `bindConcurrentSlot`/:2172 is correct. EVIDENCE: pkg/gateway/sessionserver/start.go:2140, :2172, :2602, :2761

WATCHOUT: `pkg/gateway/sessionserver/slotretry_load_test.go` declares the second `ReleaseSlotReservation` fake (`concurrentSlotBinder`, three-parameter form at :33) and it is easy to miss because the file's name does not match the deliverable. It is listed in "Files touched", so this is a note for the implementor rather than a gap. EVIDENCE: pkg/gateway/sessionserver/slotretry_load_test.go:33-37

UNVERIFIED: whether `ClaimSlot`'s pass-1 admitting a pod the WarmPoolController is already draining (pass 1 reads the claim, never the phase) is a defect worth its own problem statement. It is pre-existing and outside this proposal, and no lens here has been asked to judge it. A conformance lens after implementation should decide.


### [non-spec-recheck-2.4.review-mechanism.1]

DECISION: Returned an empty findings list for the end-to-end-mechanism lens on this round — BECAUSE I traced the two mechanism changes in the delta (CODE-2's rollback status code `FailedPrecondition` → `Aborted`, and the compensating `Shutdown`'s `deadlineMs` 0 → `budget/2`) from origin to final effect and both hold, and every citation the two new rationale paragraphs added resolves exactly — ALTERNATIVES: I built and dropped four candidates, each listed below as a WATCHOUT or a MISTAKE-avoidance note, because each either failed verification or was a close variant of something already refuted.

FACT: The `budget/2` split is mechanically correct end to end. `adapterclient.Client.Shutdown`'s fourth parameter is a `time.Duration` (not an int ms), converted with `int32(deadline.Milliseconds())` — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:817. The adapter passes `req.GetDeadlineMs()` into both `drainViaLifecycle` and `contextWithGraceDeadline`, and `contextWithGraceDeadline` returns the parent unchanged for a non-positive grace — EVIDENCE: pkg/adapter/session.go:260,:263,:327-332. `resolveShutdownGrace` prefers the ctx deadline over the 10s default, so the close's grace becomes budget/2 and the socket runtime returns after its own Kill rather than on ctx expiry, which is exactly what makes the gateway see a real outcome instead of a timeout — EVIDENCE: pkg/adapter/mcpruntime.go:312-324; pkg/adapter/socketruntime.go:456-467.

FACT: The `deadlineMs: 0` the shipped end-of-session path sends is a live schema violation this proposal's split incidentally avoids on its own path. `RuntimeOps` frames marshal `DeadlineMs int32` with `json:"deadlineMs,omitempty"`, so a zero value is OMITTED, while the frame schema has `required: ["type","deadlineMs","reason"]` and `minimum: 100`. `Binder.ReleaseSlot` sends `Shutdown(ctx, sessionID, "", 0)` today. EVIDENCE: pkg/adapter/runtimeops.go:65; schemas/runtime-ops-events.schema.json:180-183; pkg/gateway/podlifecycle/podsession/slotbinder.go:542. This is pre-existing and outside 0081's subject; I did not file it.

FACT: `slotCleanupBudget` cannot divide by zero in production. `SlotBindRequest.MaxConcurrentSessions` is only built under `match.MaxConcurrentSessions > 1` (start.go:2139,:2351,:2477-ish) and `ResumeRequest.MaxConcurrentSessions` is normalized with `maxConcurrentSessions(...)` at the one construction site. EVIDENCE: pkg/gateway/sessionserver/start.go:2139-2140, :2351-2353, :4029; pkg/gateway/podlifecycle/podsession/binder.go:640-645. A future test that builds a bare `SlotBindRequest` and drives `materializeSlot` would panic, but that is a fixture hazard rather than a staged defect — I judged filing it speculative.

WATCHOUT: The `Aborted` rollback's status code is almost never observed by the gateway on the compensation's own path. The compensation only fires AFTER `materializeSlotStages` returned an error, so on the class-three race the gateway's `StartSession` already ended in `DeadlineExceeded` and the rollback's answer is discarded by gRPC. The reachable observers are (a) a §11.4 revoke racing a live `StartSession`, (b) `Binder.Launch`'s exclusive `StartSession` at binder.go:1013, which shares the same adapter call site, and (c) the staged tier-7a rendezvous. The proposal's rationale is still true for those, so this is not a defect — but do not file "the code is unobservable"; it is observable on (a)-(c). EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1013; slotbinder.go:313.

WATCHOUT: `pkg/adapter/session.go:163` (`noteRuntimeStarted` in `StartSession`) is reached by BOTH the concurrent `materializeSlot` start stage and the exclusive `Binder.Launch` (binder.go:1013, the `else if` arm). CODE-2's rollback therefore also fires on the exclusive path, where `reclaim()`/`failPhase` retires the pod. The proposal never claims the exclusive path is untouched by CODE-2, and the outcome is fail-closed, so this is not a finding — but a reader who assumes "sdkwarm is the only exclusive site" will misread it. `sdkwarm.go:261` is reached by `ConfigureWorkspace` at binder.go:1009, the OTHER arm of the same if/else. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1008-1023; pkg/adapter/sdkwarm.go:249-262.

FACT: Every citation the two new paragraphs added is exact, including the ones most likely to be off by one. `slotfailure.go:91-99` is precisely the `PermissionDenied, FailedPrecondition` case through its `return SlotReasonPolicyRejection`; `:100-101` is precisely `default:` / `return SlotReasonTransient`; `:41-48` is `NonRetryable`. `start.go:2172` is inside `claimAtCreate` (2085-…) and `:2602` inside `bindConcurrentSlot` (2594-2610), which is the earlier round's confirmed attribution error now fixed. `slotretry_test.go:69` is the `req` helper's by-value return; `slotretry_load_test.go:84-87` is the goroutine-body call. `session.go:243,259-261` is now correct (an earlier round caught `:242`). EVIDENCE: as cited.

MISTAKE-avoidance: I nearly filed "the tier-1 per-stage case cannot assert `deadlineMs` strictly less than the RPC deadline, because `concurrentAdapter`/`recordingShutdownAdapter` discard the ctx". `recordingShutdownAdapter` records the full `*ShutdownRequest` (so `DeadlineMs` is assertable directly), and capturing `ctx.Deadline()` is a one-line fixture addition with no design content. The identical class of finding was already refuted this loop ("tier-7a asserts adapter-internal state no test at its tier can read"). EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:1156-1173; slotbinder_test.go:71-104.

UNVERIFIED: On an exclusive pool reached through `Binder.Resume`, `slotCleanupBudget` degenerates to the whole pool `cleanupTimeoutSeconds`, and `compensateFailedSlotBind` runs SYNCHRONOUSLY on a `context.WithoutCancel` inside the branch that holds the client's resume request. A pool with a large `cleanupTimeoutSeconds` therefore blocks the resume handler for that long even after the client goes away. The proposal names the degeneration explicitly (non-spec-changes.md, the split-direction paragraph) and argues it is why the split takes the window out of the budget rather than widening it, so the budget itself is unchanged by this round. Nobody has checked whether any realistic pool sets `cleanupTimeoutSeconds` high enough for this to matter, or whether an upper clamp belongs on the gateway side. A later operational or resource lens should price it.

USEFUL [Standing context → Traps, the `codes.Aborted` DECISION and the `queue` re-entry entry]: both saved me a full re-derivation. The Aborted entry already carried `slotfailure.go:41-49,:84-102` and `start.go:2761-2780,:2873-2879`, which is where the new rationale paragraph's citations land; I only had to re-open them rather than rebuild the argument.


### [non-spec-recheck-2.4.review-operational.1]

DECISION: returned an empty findings list — BECAUSE the whole delta this round exists for (the
`deadlineMs` split in `compensateFailedSlotBind`) was verified end to end against the tree and
every claim in it resolves, and the operational surfaces my lens owns (alerts, metric
inventories, CRD conditions, operator docs) are either untouched or already-adjudicated
pre-existing gaps — ALTERNATIVES: I built and dropped three candidates, each recorded below.

FACT: the r4/r3 snapshots are byte-identical to the live proposal AGAIN; the newest snapshot
that differs in the staging files is `non-spec-recheck-2-r2-prefix`. `diff -rq` over the whole
`scratchpad/cp-snap/0081` directory first, then take the newest differing one. — EVIDENCE:
scratchpad/cp-snap/0081/non-spec-recheck-2-r3 and -r4 both diff clean against
proposals/0081_.../ except the review log.

FACT: the round's delta is exactly two hunks in `.non-spec-changes.md` (the `slotCleanupBudget`
doc comment, `compensateFailedSlotBind`'s fourth argument moving from `0` to `budget/2` plus two
new rationale paragraphs, and the tier-1 per-stage assertion) and one bullet in `.summary.md`
(the budget decision). Nothing else moved. — EVIDENCE: non-spec-changes.md:380-386,:402-424,
:433-456,:838-846; summary.md:81-87.

FACT: every citation the new text mints resolves. `pkg/adapter/session.go:260` really is
`s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())`; `:263` is
`contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)` around
`s.Runtime.Close`; `schemas/runtime-ops-events.schema.json:180` is
`"deadlineMs": { "type": "integer", "minimum": 100 }` with `:183` the `required` line;
`cmd/lenny-gateway/user_revocation.go:45` is `userRevokeReason`, `:50` `userTerminateDeadline =
10 * time.Second`, `:55` `userTerminateRPCTimeout = 20 * time.Second`, `:128-129` the
`WithTimeout(ctx, userTerminateRPCTimeout)` + `Shutdown(..., userTerminateDeadline)` pair. The
2:1 relation the proposal claims as precedent is exactly the shipped 20s/10s pair.

FACT: the mechanism the delta stages works. `contextWithGraceDeadline` (session.go:327-332)
returns the parent unchanged for a non-positive grace, so with the OLD `deadlineMs: 0` the
runtime close inherited the gateway's own `rctx` deadline (`budget`) and `resolveShutdownGrace`
(mcpruntime.go:312-324) returned the full remaining budget — the gateway's give-up and the
adapter's SIGKILL pivot were the same instant. With `budget/2` the close's grace is half and the
RPC deadline strictly outlasts it, which is what the fix claims. `SocketRuntimeProcess.Close`
reads that grace at socketruntime.go:456 (`resolveShutdownGrace(ctx, 0,
defaultSocketShutdownGrace)`), so all three RuntimeProcess implementations honour it.

FACT: the alert half of this lens is still structurally inert after the delta, re-derived. No
alert in `pkg/alerting/rules` references any slot metric; `lenny_slot_failure_total` and
`lenny_slot_pod_replacement_total` are the only slot rows in spec/16 (:14,:15) and
docs/reference/metrics.md (:166,:167), and both stay true (the new emitters are still
`maxConcurrentSessions > 1` session-mode pods). `lenny_adapter_leaked_slots` remains named in
spec/05:545 and spec/06:160 and absent from both inventories, which is pre-existing and which
SPEC-3's new "surfaced on the `lenny_adapter_leaked_slots` gauge" clause does not worsen,
because §5.2 already names the gauge in the same bullet the clause points at.

FACT: the staging writes no CRD condition and no status subresource. A grep for
`Condition|status.phase|Status\.` over `.non-spec-changes.md` returns only unrelated
`FailedPrecondition` prose, so the §4.6.3 half of this lens is not engaged at all; the only
kube-visible write is the existing `lenny.dev/drain-request` stamp through `DrainSandbox`, which
the proposal adds callers to rather than changing.

MISTAKE (nearly filed, three dresses this round, all dropped — do not re-derive):
(1) "`budget/2` at the 5s floor gives the adapter 2.5s, below §5.2's `minimum 5s enforced at
runtime by the adapter`". Dies on the reading: §5.2's 5s minimum is on the SLOT CLEANUP timeout
(workspace tree, process group, `slotId`), while `deadline_ms` bounds only `Runtime.Close`,
which SPEC-1 makes a separate teardown. The RPC deadline still carries the full §5.2 figure.
EVIDENCE: spec/05:545; pkg/adapter/session.go:263.
(2) "the reclaim's `terminate` frame now carries a value no operator doc anticipates". No spec
or doc pins the value; every `deadlineMs` occurrence in `spec/` and `docs/` is either the
interrupt/checkpoint frame or an illustrative `10000`
(docs/runtime-author-guide/lifecycle.md:322,:327; docs/api/internal.md:428), and §28.5.3's
degradation bullet states only "SIGTERM on timeout" with no numeric bound (spec/28:1126).
The delta in fact REPAIRS the standing OPEN that every shipped gateway caller sends 0, which
`omitempty` drops from a frame whose schema requires it (pkg/adapter/runtimeops.go:65,:489).
(3) "`SocketRuntimeProcess.Close`'s doc comment now names one gateway window
(`the gateway's §11.4 step-3 10s window`, socketruntime.go:431-433) while a second caller pins
a pool-derived one". Real, and it is a Go doc comment — the same class the standing trap kills
("Do not file the `sessionserver.go` collaborator doc-comment narrowing").

WATCHOUT: open decision 11's sentence "the reclaim's §5.2 cleanup budget bounds the gateway's
wait rather than the adapter's teardown" (summary.md:259) is now looser than it was: after the
delta the call DOES bound one component of the adapter's teardown, the runtime close. Its
load-bearing half survives, because the part that races a retry is `removeSlotTree`, which no
deadline bounds. I did not file it (decision-entry prose, and the conclusion is unchanged), but
a fixer editing that entry for any other reason should tighten "the adapter's teardown" to "the
adapter's tree removal".

UNVERIFIED: whether the tier-1 per-stage case can observe "strictly less than the RPC deadline
that same configuration produces" (non-spec-changes.md:842-843) without the fake reading
`ctx.Deadline()` inside its `Shutdown` handler. The fixture work paragraph already stages
per-request recording on `concurrentAdapter`; recording the remaining deadline is one more
field. A test-mechanics lens, not this one, should confirm the assertion is writable as stated.


### [non-spec-recheck-2.4.review-performance.1]

DECISION: Returned an EMPTY findings list for the performance / scalability / failure-mode lens — BECAUSE the round's whole delta is the `compensateFailedSlotBind` deadline split, every load-bearing claim in it verifies against the tree, and the three capacity questions my lens owns (write amplification, hot-key/serialization, store-outage failure modes) were each quantified and land far below every budget the spec states — ALTERNATIVES: filing the exclusive-pool resume budget degeneracy (already Settled at review-log:216 and re-argued in the delta's own prose), the reserved-branch/re-attach drain amplification under a blob-store or Redis outage (stated by the proposal itself as the conformance gap closing, and a close variant of the withdrawn dress at review-log:381 item 3), and `slothealth`'s never-evicted maps (withdrawn at review-log:342). All three would have been re-files.

FACT: The delta's five factual claims all verify. `Client.Shutdown(ctx, sessionID, reason, deadline time.Duration)` puts `int32(deadline.Milliseconds())` on the wire, so `budget/2` is well-typed — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:818. The adapter passes `deadline_ms` straight into the `terminate` frame — EVIDENCE: pkg/adapter/session.go:260 (`drainViaLifecycle(req.GetDeadlineMs(), …)`), :299-305. The schema's `deadlineMs` minimum is 100 and it is required — EVIDENCE: schemas/runtime-ops-events.schema.json:180,:183; the 5s floor puts `budget/2` at >= 2500ms on every pool. The shipped §11.4 pair really is 20s RPC / 10s grace — EVIDENCE: cmd/lenny-gateway/user_revocation.go:50,:55,:128-129.

FACT: The delta makes the compensation STRICTLY better than the shipped teardown on the wire-minimum question, which is worth knowing before anyone files on it. The shipped end-of-session `Binder.ReleaseSlot` sends `deadline 0`, so `drainViaLifecycle(0, …)` emits a `terminate` frame carrying `deadlineMs: 0`, below the schema's own minimum of 100. That is a PRE-EXISTING defect on a path 0081 does not open — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542; pkg/adapter/session.go:259-261; schemas/runtime-ops-events.schema.json:180.

FACT: §5.2's "(minimum 5s enforced at runtime by the adapter)" clause has NO implementation. `grep -n "cleanupTimeout\|CleanupTimeout" pkg/adapter/*.go` returns only the WHOLE-POD scrub's `CleanupTimeout` (pkg/adapter/podscrub.go:128); there is no per-slot cleanup-timeout enforcement anywhere in the adapter. So pinning `budget/2` as `deadline_ms` does not halve any figure the adapter enforces, and the "graceful window is not the §5.2 cleanup timeout" reading the delta relies on is sound — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; pkg/adapter/podscrub.go:128.

FACT: Top-tier write math for this change, so nobody re-derives it. The compensation fires once per FAILED bind, never per session. §16.5 gives Tier 3 an implied claim rate of `10,000 / 333 ~= 30/s` and a session-creation success SLO of 99.5%, so the compensation's steady-state rate is <= ~0.15/s. Each firing costs one `Shutdown` RPC on an already-open connection, one Redis slot-counter op that the path already made, and at most one idempotent `lenny.dev/drain-request` annotation patch. Against §16.5's ~365 ops/s Redis figure and the etcd status-write budgets this is noise. No net-new watch, no new informer cache, no new hot key — EVIDENCE: spec/16_observability.md:606 (claim rate and ops/s reconciliation), :632-641 (SLO table).

WATCHOUT: `slotCleanupBudget`'s stated body is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` and the staging gives only its signature. A literal integer division panics at `maxConcurrentSessions == 0`. Production is safe on both callers — `SlotBindRequest` reaches `materializeSlot` only for `> 1`, and `resumeOnPod` normalizes with `maxConcurrentSessions(match.MaxConcurrentSessions)` to a minimum of 1 — but a tier-1 fixture that builds a zero-valued request would panic. Not filed (implementor detail on a bounded blank), worth a guard when S9 is built — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:640-645 ("normalized to a minimum of 1 by the caller"); pkg/gateway/sessionserver/start.go:4029.

FACT: The band argument that decides whether the split is a reliability regression, checked so it is not re-derived. Under the old `deadlineMs: 0` form the adapter's close ctx WAS the inbound RPC ctx, so a runtime exceeding `budget` was killed at the same instant the gateway gave up and the answer was lost — `leaked`. Under the split a runtime exceeding `budget/2` is killed at `budget/2`, the handler still has `budget/2` for `removeSlotTree` and the response, and the gateway records `!cleanly` — also `leaked`. The classification for the slow-runtime band is identical; what changes is that the FAST band now reports a completed cleanup as completed. No disposition gets worse — EVIDENCE: pkg/adapter/session.go:263-265, :327-332; pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543.

USEFUL [Standing context / Traps, review-log:216, :342, :381]: the three recorded near-filed performance dresses (exclusive-pool resume budget vs the §16.5 creation SLO which samples successful creates only; the unwired `SpawnPath` SIGTERM pivot; the `ExcludePods` FIFO / warm-pool-headroom / Redis-drain-storm trio) saved this pass from re-filing all three. Each is still accurate against the tree.


### [non-spec-recheck-2.4.review-reliability.1]

DECISION: returned an EMPTY findings list — BECAUSE this round has NO delta at all. `diff -rq`
over every snapshot in `scratchpad/cp-snap/0081` shows `non-spec-recheck-2-r3`,
`non-spec-recheck-2-r3-start`, `non-spec-recheck-2-r4` and `non-spec-recheck-2-r4-start` are
all byte-identical to the live proposal; the newest snapshot that differs is
`non-spec-recheck-2-r2-prefix`, and that delta (the `deadlineMs = budget/2` split) is exactly
the one `[non-spec-recheck-2.3.review-reliability.1]` already traced end to end and cleared.
I re-derived the whole trace independently rather than trusting it, and it holds.
ALTERNATIVES: (a) filing the 2.5s graceful window on an exclusive-pool `Binder.Resume` reclaim
against the shipped 10s `defaultSocketShutdownGrace` — dropped, because a SIGKILL at the pivot
still returns `closeErr == nil` from `SocketRuntimeProcess.Close` (it returns
`p.listener.Close()` after `<-done`) and from `MCPRuntime.Close` (explicit `return nil` on the
`time.After` arm), so the reclaim is still classified clean and no residue survives; the
abandoned session has no work to quiesce. (b) re-filing the gateway-replica-death window on
`ExcludePods` — dropped as a strict subset of the already-refuted gateway-death case and of
the recorded create-time-reserved accepted failure mode.

FACT: the delta's premise — "a runtime that uses its grace is no longer reported unacknowledged"
— is sound because NEITHER runtime `Close` errors on grace expiry.
`SocketRuntimeProcess.Close` does `select { case <-done: case <-time.After(grace): Kill; <-done }`
then `return p.listener.Close()`; `MCPRuntime.Close`'s `time.After` arm is `Kill; <-done; return nil`.
Only a natural non-zero exit (`exitErr`) or a listener-close failure yields `closeErr != nil`.
So `budget/2` shortens the pivot without manufacturing a `leaked` classification.
EVIDENCE: pkg/adapter/socketruntime.go:435-467; pkg/adapter/mcpruntime.go:294-305.

FACT: there is no divide-by-zero on the production resume path. `resumeOnPod` builds its
`ResumeRequest` with `MaxConcurrentSessions: maxConcurrentSessions(match.MaxConcurrentSessions)`,
which normalises to a minimum of 1, so `slotCleanupBudget`'s denominator is >= 1 at the new
`Binder.Resume` call site as well as at the two bind sites. This closes the production half of
the `[non-spec-recheck-2.3.review-reliability.1]` WATCHOUT; that WATCHOUT's fixture half
(three `podsession.ResumeRequest{...}` literals in binder_test.go leaving the field zero)
still stands and is what an implementor must fix.
EVIDENCE: pkg/gateway/sessionserver/start.go:4030-4031.

FACT: `ReleaseSlotReservation` has exactly SIX production sites and CODE-4's call-site table
names all six. `applySlotRetryPolicy` (start.go:2834), `rollbackClaim` (start.go:3246),
`ClaimSlot`'s connect-stage release (slotbinder.go:172), `BindReservedSlot` (slotbinder.go:217),
`releaseResumeSlot` (binder.go:1714), plus the `slotBinder` interface declaration
(start.go:2727). No seventh site exists; a lens counting them can stop here.
EVIDENCE: pkg/gateway/sessionserver/start.go:2727,:2834,:3246; pkg/gateway/podlifecycle/podsession/slotbinder.go:172,:217; binder.go:1714.

FACT: the staged wrapper's `errors.As(err, &sbe)` guard is TOTAL over today's
`materializeSlotStages`. All five stage failures return `b.slotBindError(...)`, which is
`return &SlotBindError{...}` — a pointer, so `errors.As` always matches and no post-connection
failure escapes the compensation today. The proposal's unconditional `releaseCredentials`
outside that guard is future-proofing rather than a live gap.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:483-485; the five
`return nil, b.slotBindError(...)` sites at :286-324.

FACT: no gRPC retry can double-deliver the compensating `Shutdown`. There is no
`WithDefaultServiceConfig`, `retryPolicy` or `MaxAttempts` anywhere under
`pkg/gateway/runtime/adapterclient` or `pkg/gateway/podlifecycle`, so the dial takes gRPC's
default (transparent retry only for a request that never left the client). A `Shutdown` that
reached the handler is never re-sent, which is what keeps `RecordSessionScrub`'s missing
per-session dedup harmless on this path.
EVIDENCE: grep over pkg/gateway/runtime/adapterclient and pkg/gateway/podlifecycle returns nothing.

FACT: the SDK-warm `_ = s.noteRuntimeStarted(...)` guarded on `fresh` does NOT open a hole in
CODE-1's `live` report gate. `fresh` is false only on the idempotent-repeat arm of
`claimSessionSlotUnderLock` (`if st.started { if idempotentRepeat { return false, ... } }`),
which means an earlier call already recorded the session in `runtimeLive`. There is no path
where a live SDK-warm session ends outside the cohort and silently withholds the
served-session increment. I nearly filed this as "a session that never advances
sessionsServed defeats maxSessionsPerPod retirement"; do not rebuild it.
EVIDENCE: pkg/adapter/slotsession.go:79-88; pkg/adapter/sdkwarm.go:217,:259-261.

FACT: `Binder.Resume`'s adapter-RPC failure branch is a SINGLE branch, and every earlier
failure (`connect`, `reserveResumeSlot`) returns before the adapter holds anything, so
CODE-4's "compensate, then close, then release" ordering has exactly one insertion point and
cannot fire on a pre-RPC failure.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1631.

FACT: `createClaimNeedsRollback`'s double-release guard keys on
`errors.As(err, &*podsession.SlotBindError)`, and CODE-4 newly folds a `*SlotBindError` into
`Binder.Resume`'s error chain. The two do not meet: both `createClaimNeedsRollback` call sites
are on the create-and-start path around `s.startOnPod` (start.go:833, :1026), never on
`resumeOnPod`'s `podBinder.Resume` branch, so the new wrap cannot flip a create-time
reservation release off. Worth recording because the guard's polarity is inverted
(`return !errors.As(...)`) and reads as a hazard at first glance.
EVIDENCE: pkg/gateway/sessionserver/start.go:833,:1026,:3211-3225.

USEFUL [Standing context / Settled 130]: the "diff -rq over the whole snapshot directory first"
rule was right for the third round running. The brief named `non-spec-recheck-2-r4`; it is
byte-identical to live, as is r3. Anyone reading only the named snapshot sees an empty diff and
loses the round.

USEFUL [non-spec-recheck-2.3.review-reliability.1]: its end-to-end trace of the
`deadlineMs = budget/2` mechanism was accurate in every particular I re-checked, which let me
spend the round on the surrounding recovery paths instead of re-deriving the delta.


### [non-spec-recheck-2.4.review-security.1]

DECISION: returned an empty findings list for the security lens on round 4 — BECAUSE every delta item verified clean against the tree and every security-shaped candidate I built either failed the materiality bar or is a close variant of an already-refuted finding — ALTERNATIVES: I built and dropped four candidates, each recorded below so nobody rebuilds them.

FACT: the delta's two design changes are `codes.FailedPrecondition` → `codes.Aborted` on CODE-2's two rollbacks, and `cl.Shutdown(..., 0)` → `cl.Shutdown(..., budget/2)`. Every citation supporting both was opened and is exact: `slotfailure.go:91-99` (the FailedPrecondition→policy_rejection arm), `:41-48` (`NonRetryable`), `:100-101` (the transient default), `slotbinder.go:322-324` (`slotFailureSessionStart` minted at the start stage), `binder.go:293` (the constant), `start.go:2761` / `:2766-2779` (`classifySlotBindFailure` pass-through), `:2873-2879` (`reason.NonRetryable() || attempt == maxSlotRetries`), `checkpoint.go:115` and `oplock.go:36-40,:82` (the adapter's only `codes.Aborted` sites), `session.go:104`, `resume.go:33,:42`, `slotsession.go:274-283`, `coordination.go:133,:281`. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:85-102

FACT: `Client.Shutdown`'s fourth parameter is a `time.Duration`, serialised as `DeadlineMs: int32(deadline.Milliseconds())`, so `budget/2` is type-correct and the five-second budget floor puts the wire value at 2500ms, above the `terminate` frame's schema minimum of 100. The adapter uses that value in exactly two places: the frame (`session.go:260`) and `contextWithGraceDeadline` around `Runtime.Close` (`:262`). EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824; schemas/runtime-ops-events.schema.json:180-183; pkg/adapter/session.go:255-266,:327-332

FACT: no divide-by-zero is reachable in `slotCleanupBudget`. `resumeOnPod` passes `maxConcurrentSessions(match.MaxConcurrentSessions)`, which normalises to a minimum of 1 (`start.go:4029`), and every slot bind route sits behind `match.MaxConcurrentSessions > 1`. I checked this because the staged helper's stated body divides by that field. EVIDENCE: pkg/gateway/sessionserver/start.go:4025-4030

WATCHOUT: the tempting security finding here is "CODE-1 widens `removeSlotTree` from the bound gate to `removed`, so a lagging compensating `Shutdown` now deletes a SUCCESSOR attempt's workspace and credential directory where before it only deregistered the entry." That is real but it is the exact hazard `ExcludePods` is staged for, and the one path `ExcludePods` does not reach (the create-time-reserved `BindReservedSlot` retry, which returns to the same pod through `row.PodAssignment`) is already recorded as an accepted failure mode. Do not file it. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224; pkg/adapter/slotsession.go:214-218

WATCHOUT: the second tempting security finding is "answering `Aborted` makes a start refused after a §11.4 revoke's `Shutdown` retryable, where `FailedPrecondition` would have been terminal." It does not clear the bar: in the SHIPPED tree `noteRuntimeStarted` has no return value and no rollback exists at all, so the racing start simply succeeds today. Aborted-plus-retry is strictly more conservative than what ships, and "less strict than it could be is not a finding". EVIDENCE: pkg/adapter/session.go:163; cmd/lenny-gateway/user_revocation.go:128-129

FACT: the §15.4.2 drain signal that CODE-1 newly withholds IS asserted. The tier-1 "Bound but unstarted" case asserts "no `terminate` frame on CH-RUNTIMEOPS" with `startRuntimeOps` attached and no other bound entry on the pod. The round that deleted the tier-4 §15.4.2 assertion did not leave the withheld signal untested, so a "changed gate with no test" finding against that deletion is refuted before it is written. EVIDENCE: non-spec-changes.md:757-767

FACT: the delta's newest shipped-test citations are all exact — `TestSlotBindErrorReason_spec_5_2` at slotretry_test.go:383-407 (the `{"session_start", codes.PermissionDenied, …}` row it wants a neighbour for is at :390), `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` at :468-492, `req` at :69, the load test's goroutine body at :82-88, and `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` at tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304 with two started co-tenants and a want of exactly one frame. The by-value/by-pointer fixture correction this round landed is accurate.

FACT: the coordinator-hold allowlist genuinely excludes `Shutdown` — it carries only `CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, and the two health methods — so the edge-case bullet claiming a held pod refuses the compensation and is classified `leaked` is correct rather than optimistic. EVIDENCE: pkg/adapter/holdstate.go:53-57

FACT: nothing in "Files touched on application (non-spec)" reaches `schemas/`, `charts/`, or `pkg/controller/sandbox/podspec`, so the programme's S-2 proto reservation and the R6 podspec/chart serialisation are both intact. The compensating `Shutdown`'s reason string `"slot_bind_failed"` mints no wire value because `drainReason` defaults every unrecognised value to `session_complete`. EVIDENCE: pkg/adapter/session.go:308-321

UNVERIFIED: the credential-lease release ordering on the compensation path (adapter-side `removeSlotTree` first, gateway-side `releaseCredentials` second, both after the reclaim's RPC) means the §4.9 leases stay live for up to the whole budget — which on an exclusive-pool `Binder.Resume` degenerates to the pool's full `cleanupTimeoutSeconds` — while the client's request is held. I judged this not a security finding (the leases are returned unconditionally on the error path, so it fails closed) and not new in this delta, but nobody has checked whether any §4.9 or §13 statement bounds how long a failed attempt may hold a minted lease. A credential-lifecycle lens should confirm. EVIDENCE: non-spec-changes.md:498-517; pkg/gateway/podlifecycle/podsession/binder.go:1259-1281


### [non-spec-recheck-2.4.review-test-coverage.1]

DECISION: returned an EMPTY findings list. — BECAUSE the round's whole delta (the
`deadlineMs: 0` → `budget/2` grace split in `compensateFailedSlotBind`, plus its two
rationale paragraphs and the summary bullet) carries its own test change in the same
edit: the tier-1 "Per-stage compensation table" bullet now asserts a positive
`deadlineMs` equal to half the budget AND strictly less than the RPC deadline the same
pool configuration produces, which is exactly the invariant the split installs. Every
other behaviour CODE-1..CODE-5 change has a named case at a reached tier, and the four
candidate gaps I derived independently all turned out to be already tried and withdrawn
in the standing context's Traps. — ALTERNATIVES rejected, each with the reason:
(1) "no test pins that a runtime consuming its full grace is still recorded
`Leaked=false`" — the value-ordering assertion IS that invariant, and a wall-clock
variant is a sleep test; the standing Open at review-log.md:446 ("Nothing at any tier
pins that the compensating `Shutdown` stays inside its budget") already records this as
deliberately not filed. (2) "the compensating reclaim's `terminate` frame has no
assertion that its `deadlineMs` clears the schema `minimum: 100`" — Traps item "four
test-coverage dresses" (2) declined the sibling reason-enum version, and the ≥2500ms
floor is arithmetic off `slotCleanupBudget`'s 5s floor, whose own missing test a prior
round refuted. (3) "the `relErr != nil` arm of the new `ExcludePods` exclusion has no
listed case" — the listed retry case covers `sbe.Leaked` true and clean-release; the
extra arm is the conservative half a prior verification already called defensive
hardening. (4) tier 5 / tier 8 / tier 9 / tier 3 omissions — all four declined by
earlier rounds (Traps lines on tier 5 and tier 3; standing Open "Is tier 8 reached?";
refuted-list entry on tier 9).

FACT: the proposal's test-disposition accounting ("besides
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`, no existing adapter
test drives `Shutdown` for an unbound or unstarted entry") is TRUE, and it also holds
outside `pkg/adapter`, which the sentence does not claim. I enumerated every `.Shutdown(`
caller under `tests/` and every `noteRuntimeStarted` caller in the tree. Every one drives
a started entry, an already-released session, or a session the adapter never held:
tier-10 `recycle_scrub_conformance_test.go:186,:386,:454` StartSession first and `:274`
holds no entry at all; `tests/tier2_component/warmlayout/warm_layout_test.go:161`,
`tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:145` (via
`startAndShutdownSlot`), and `tests/tier4_integration/concurrent_delegation_proxy_test.go:424`
all StartSession first. `pkg/adapter/drain_test.go:23` looked like the exception and is
not: `claimSessionForTest` is `claimSessionSlot` + `noteRuntimeStarted`
(`pkg/adapter/export_test.go:41,:45`), so `st.started` is set. Do not re-derive this list.

FACT: `usage_test.go:233`'s `noteRuntimeStarted("sess-wire")` is preceded by
`bindSessionForTest(t, s, "sess-wire")` at `:229`, and each of
`podmcp_arming_internal_test.go:84,:185,:230` is preceded by a `claimSessionSlot("alice", …)`
in the same function, so CODE-2's confirmation guard leaves all five green and only
`adapterevents_test.go:95,:184` need the new `bindSessionForTest` call. The proposal's
scope paragraph is accurate on every one of these.

FACT: `slotBindRequest` DOES populate `CleanupTimeoutSeconds`
(`pkg/gateway/sessionserver/start.go:2566-2567`), despite the neighbouring
`exclusiveBindRequest` comment at `:2524-2529` saying "the concurrent slotBindRequest
omits them" — that comment describes `CleanupCommands`/`CleanupTimeoutSeconds` on the
session-mode request and reads as if it excluded the concurrent one. The summary's
"`SlotBindRequest` already carries both inputs" is therefore true, and a reviewer who
reads only that comment will wrongly conclude the budget always falls to its 5s floor.
`ResumeRequest` carries both too (`pkg/gateway/podlifecycle/podsession/binder.go:640-667`).

FACT: `slotCleanupBudget` cannot divide by zero on either caller. `ResumeRequest`'s bound
is normalised through `maxConcurrentSessions()` at `pkg/gateway/sessionserver/start.go:4029`
(`< 1 → 1`, `:3353-3358`), and `slotBindRequest` is only built for a pool the dispatcher
has already resolved as `maxConcurrentSessions > 1`. I checked this because `budget/2`
made the divisor newly load-bearing; it is not a defect.

FACT: the four tier-11 `Shutdown`-row gates all key on substrings SPEC-1 leaves standing.
`recycle_scrub_trigger_consistency_test.go:74-100` requires "recycle disposition",
"ReportPodScrub", the three recycle params, and "does not block the response on the
scrub" — every one of them in the row remainder SPEC-1 explicitly does not replace
("leaving the remainder of the row … unchanged"). `spec_47_rpc_row_naming_test.go:139`
only requires a `Shutdown` row to exist. So no shipped tier-11 gate goes red on SPEC-1,
and none catches the `docs/reference/adapter-contract.md:75` drift either, which is the
already-recorded DEFERRED rather than a test gap.

FACT: the tier-11 plan for DOCS-1 is mechanically sound against the shipped file.
`generalSlotEdges` (`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37`)
feeds a positive loop over `generalBlock` (`:56-60`) and a negative loop over
`scopedBlock` (`:69-73`), and `scopedBlock` is sliced as
`s62[index(scopedHeader):index(generalHeader)]`, so SPEC-4 placing the new edge in the
either-concurrency block satisfies both loops with one list entry, exactly as the
proposal states. The paired substring `` `receiving_uploads` | `slot_cleanup` `` matches
DOCS-1's row and is discriminating, because the existing `requireAllContain` list
(`:104-111`) carries the two names only separately.

WATCHOUT: `recordingShutdownAdapter` (`pkg/gateway/podlifecycle/podsession/binder_test.go:1156-1173`)
records the `*ShutdownRequest` and NOTHING about the RPC context, so the delta's new
"strictly less than the RPC deadline that same configuration produces" assertion needs
the fake to capture `ctx.Deadline()`. The fixture paragraph at non-spec-changes.md:824-836
enumerates the fake's new capabilities (per-stage error injection, `PrepareWorkspace` and
`AssignCredentials` handlers, per-request recording) and does not name that one. I did
NOT file it: it is the same class as the tier-7a "no seam is staged" finding a prior
round refuted, being a behaviour-free one-field addition in the same package. A fixer
already editing that paragraph should add the clause; a reviewer should not spend a
finding on it. EVIDENCE: binder_test.go:1167 (`func (a *recordingShutdownAdapter) Shutdown(_ context.Context, req …)`).

USEFUL [Traps, "MISTAKE nearly filed, four test-coverage dresses"]: items (2) and (3)
killed two candidates I had independently derived and half-written (the
`slot_bind_failed`/`session_complete` frame assertion, and the
`isTransientPodClaimError` classification-preservation claim on `Binder.Resume`'s new
`*SlotBindError` wrapper). Reading Traps before writing saved both.

USEFUL [Open, "Nothing at any tier pins that the compensating `Shutdown` stays inside its
budget"]: this is the exact residual the delta's fix shard flagged as still open, and its
recorded reason for not filing ("a prior round refuted the `slotCleanupBudget` has no
listed test finding as an extra case inside a tier the proposal already exercises") is
what stopped me re-filing the wall-clock variant under the new grace split.

### [f5.open-decisions.other-proposals.0078]

DECISION: the `## Impacts on other proposals` row for 0078 stands, with one clause added. Disposition `impact-row`, unchanged. summary.md's 0078 row now closes its test-surface sentence with "it edits no file 0078's TEST-7 rewrites, so that last overlap is package co-location rather than a file collision", which distinguishes the `tests/tier7a_load_local/` overlap from the two genuine file collisions in `pkg/adapter/socketruntime_test.go` and `tests/tier4_integration/concurrent_workspace_test.go`. Nothing else in the row changed; the ordering column still reads "Land after this one, and amend the sibling assertion's listener half when 0078's CODE-1 removes the listener close."

FACT: every anchor in the row re-verified against the tree and against 0078 this firing. `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` is at `pkg/adapter/socketruntime_test.go:252` and its two `defer sp.Close(context.Background(), "sess-b")` cleanups are at `:258` and `:316`, which is what 0078:573-574 converts. `return p.listener.Close()` is at `pkg/adapter/socketruntime.go:467`, which is what 0078 §7.1 (`0078:424-430`) replaces. The sibling early return is at `pkg/adapter/socketruntime.go:441-446`. `tests/tier4_integration/concurrent_workspace_test.go:126` is the `t.Cleanup(func() { _ = rt.Close(context.Background(), "pod-teardown") })` line 0078's TEST-5 converts. 0078 heads itself "Draft for review." with "**Date:** 2026-08-25", and its checklist (`0078:87-102`) carries exactly CODE-1, CODE-2, TEST-1 through TEST-7 and DOCS-1.

FACT: the tier-7a overlap is package-level only. 0078's TEST-6 is a new case in `tests/tier7a_load_local/` and TEST-7 rewrites `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` (`0078:784-785`). This proposal's own tier-7a text states that file's two shipped cases "must keep passing unchanged" (non-spec-changes.md, the tier-4-and-7a test section) and its files-touched list names the package rather than that file, so no file in it is opened here.

FACT: this proposal opens neither production file 0078 changes. `pkg/adapter/socketruntime.go` and `cmd/lenny-adapter/main.go` are absent from the non-spec files-touched list, and so is `docs/runtime-author-guide/lifecycle.md`, DOCS-1's only target (`0078:787`), so every 0078 deliverable keeps its subject.

CORRECTS [problem-statement.md, the WHY IT MATTERS AND WHY NOW paragraph]: it read "Proposal 0078 exists to make pods survive across sessions, which widens exactly this exposure, so this is sequenced ahead of it." That is the premise the impacts row already records as withdrawn, and leaving the two disagreeing is the defect. 0078's own fixed decision states the opposite: "After it lands, a recycling sidecar pod still serves one session, and it fails at the accept timeout rather than by dialing an address that no longer exists" (`0078:46-48`). The sentence now says 0078 keeps the pod's runtime listener bound across a session teardown, does not widen this exposure, and is sequenced after this one for the test-file reason the impacts row records. The sequencing conclusion is unchanged, so the SEQUENCING paragraph's "Proposals 0078 and 0079 ... are sequenced after this one" still holds and was not touched.

WATCHOUT: the row's maintenance obligation is asymmetric and easy to over-apply. 0078's CODE-1 kills only the listener half of the staged co-tenancy-hazard assertion. Its connection-close and child-kill halves still discriminate after 0078 lands, because 0078 leaves the occupancy gate, the shared-connection close and the graced child reap untouched (`0078:22-25`). Whoever lands second amends one third of that assertion.

DEFERRED [nothing]. No staged deliverable is added, removed, merged, split or resequenced, so the implementation checklist is unchanged and every box stays unticked.


### [f5.cleanup]

FACT: summary.md already carried exactly the required sections, in the required order, and nothing else. `# Summary: A failed session bind leaves a stale adapter slot registry entry`, then `## Summary` holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**` and `**Watch out for.**` in that order and carrying no prose of its own, then `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and `## Deliverable index` last. No rewrite was owed and the file is unchanged by this firing.

FACT: nothing was relocated, because there was nothing to relocate. No `### Retired` or equivalent block sits inside `## Open decisions for human to make`, no meta-list of staged items with a proposed disposition survives anywhere in the file, and the `**Watch out for.**` part holds seven implementation hazards rather than an errata list of corrections owed to files this loop cannot edit.

FACT: `## Open decisions for human to make` carries entries 9 and 11 and only those, which is what this firing left open. Entry 9 stood at the gate and needed no edit. Entry 11's proposed resolution was refuted at the gate and was not applied, so the entry stays the human's. Both keep their stamped identifiers verbatim.

FACT: the section's preamble reads true against the entries below it, so it was left as it stands. It claims the loop routed both to a human, that each states its question and its ground, that entry 9 carries a recommendation with its alternatives and a confidence, and that entry 11 states no recommendation. Entry 9 carries the recommendation, both closure forms with why each lost, the cost of deciding otherwise, and a low confidence; entry 11 carries the question and its ground and no recommendation.

FACT: `## Defects in the shipped tree that this proposal does not stage` carries eight entries, one per out-of-scope-stands item this firing carried, verbatim and unreworded: the `claimPodMCPStartLocked` entry-count gate, the false `SocketRuntimeProcess.Close` doc comment, the pod bricked by the listener close, §6.2's missing connect-stage terminal, the lagging pre-`Runtime.Start` failure branch, the exclusive path's abandoned `Prepare`, the §10.1 coordinator hold that cannot arm, and the leaked slot's unbacked occupancy. The section holds no decision.

FACT: `## Impacts on other proposals` carries nine rows and no duplicate subject: four for 0080 (§1.2, §1.19, §1.7, and the six unaffected entries §1.1, §1.3, §1.4, §1.5, §1.16 and §1.20), one each for 0073, 0075 and 0078, and one each for remediation steps R1b and R12.

FACT: the references to another proposal outside that table agree with the row that owns each, so no merge was owed and no second assertion was created. `**Decisions.**` and Non-goals cite rule S-2's reservation of the single proto edit to step R1b and the deferral of the hold-timeout reclaim to step R12, which restate the two programme rows; the file-collision decision cites the later position covering 0080, which restates the 0080 §1.1 row; the bricked-pod defect entry names proposals 0078 and 0079 as the two halves of BUILD-GAPS finding F-5.2.33, which is an ownership attribution rather than a claim about either proposal's continued validity.

FACT: `## Deliverable index` was left exactly as it stands, in last position, line for line: SPEC-1 through SPEC-4, CODE-1 through CODE-5, DOCS-1, and the closing note that tests are not separate deliverables. Its CODE-2 line names `pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go` and `pkg/adapter/resume.go`, which matches CODE-2's own heading in non-spec-changes.md; `pkg/adapter/sdkwarm.go` appears in that file's `## Files touched on application (non-spec)` list as a call-site edit and in the 0080 §1.1 impacts row, and the two are consistent.

DEFERRED [nothing]. No correction was derived that this pass may not land, because no claim was moved and none was falsified by a move.

OPEN [nothing]. No content in the file fell outside the listed sections, so nothing was left unplaced and nothing was dropped.


### [non-spec.1.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE every staged anchor resolves uniquely against the tree, the checklist covers every deliverable exactly once with backward-only Depends-on lines and single lanes, and no code step consumes a spec statement staged by a later step. ALTERNATIVES: three candidates were built and dropped, each recorded below with what killed it.

FACT: every SPEC anchor was re-verified this round and all match verbatim and resolve uniquely. EVIDENCE: spec/04_system-components.md:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening two sentences), :854 (§4.7.9 step 5); spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**` action-list sentence), :555 (`**Max retries:**` pod-selection sentence); spec/06_warm-pod-model.md:152-153 (`receiving_uploads ──→ running`), :158 (`**`reserved` hold semantics.**`), :234 (mid-resume cancel clause, `grep -c` = 1); spec/07_session-lifecycle.md:23 (atomicity parenthetical), :210 (§7.2 preamble premise), :213 (step 2 tail), :214 (step 3), :414 (§7.3 list item 4); spec/29_communication-scenarios.md:711 (§29.4 step 13 tail).

FACT: the §29.4 step-13 anchor string occurs three times in spec/29 (`:645`, `:711`, `:982`) and only the instruction's "§29.4's numbered step 13" scoping makes it unique. §29.4 spans :575-762 and step 13 is :704-711. EVIDENCE: spec/29_communication-scenarios.md:704-711. This restates a standing WATCHOUT; it still holds after this round's edits.

FACT: every §7.1 insertion-point claim checks out physically. The atomicity paragraph is one physical line INSIDE the fenced flow listing, and the line continuing it is `                         (executionMode, isolationProfile, scrubPolicy summary)`. EVIDENCE: spec/07_session-lifecycle.md:23-24. Markdown links inside that fence are already the shipped convention (the atomicity paragraph carries `[§6.2](...)` and `[§7.2](...)`), so the staged paragraph's links are not a new defect.

FACT: all six production `ReleaseSlotReservation` call sites are in CODE-4's table, with no seventh. EVIDENCE: slotbinder.go:172 (ClaimSlot connect-stage), :217 (BindReservedSlot), binder.go:1714 (releaseResumeSlot), start.go:2727 (interface), :2834 (applySlotRetryPolicy), :3246 (rollbackClaim, confirmed inside `rollbackClaim` at start.go:3241). The two test fakes at slotretry_test.go:52 and slotretry_load_test.go:33 are both on the files-touched list.

FACT: all nine `noteRuntimeStarted` call sites are enumerated by CODE-2 with no omission. EVIDENCE: production at pkg/adapter/session.go:163, resume.go:144, sdkwarm.go:261; tests at export_test.go:45, usage_test.go:233, adapterevents_test.go:95 and :184, podmcp_arming_internal_test.go:84, :185, :230. `export_test.go:45` sits inside `ClaimSessionForTest`, which calls `claimSessionSlot` first, so the guard is satisfied there — the CODE-2 fixture accounting is accurate.

FACT: CODE-1's `live` gate breaks no shipped adapter scrub test, and the reason is that every shipped fixture reaches `noteRuntimeStarted`. `sessionscrub_emit_test.go` (five `ReportSessionScrub` assertions) drives `startSlot`/`startRecycleSession`, both full `StartSession` calls (sessionscrub_emit_test.go:73-81, podscrub_test.go:163-171), and `slotsession_test.go`'s cases drive `claimSessionForTest`, which records into `runtimeLive` (export_test.go:40-47). EVIDENCE: pkg/adapter/export_test.go:45. `sessionscrub_emit_test.go` is in no proposal file list and needs to be in none.

FACT: the tier-11 reconciliation gate is safe at every point of the checklist. `scopedBlock` is bounded at the general header (`s62[Index(scopedHeader):Index(generalHeader)]`), so adding the edge to the general block cannot trip the negative loop, and between S4 and S5 the unedited `generalSlotEdges` neither requires nor forbids the new edge. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, :55-59, :64, :70-74. `generalSlotEdges` has no other reader in `tests/`.

FACT: the §5.2 `lineContaining` redirect hazard is still stopped by capitalization alone, and the current staged text is still on the safe side. The gates anchor on `"Whole-pod replacement trigger"` and `"Session count limit"` (capitalised, in §5.2 bullets); SPEC-3's append to :453 writes only the lowercase "counts toward the whole-pod replacement trigger stated below". EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92, :155. Any future reword of the append that capitalises that phrase turns the gate red.

MISTAKE (nearly filed, and here is what kills it): "S4's tier list omits tier 11 although SPEC-4 edits §6.2, which tier-11 gates read." The omission is real, but S4's own line records the disposition ("Tier 11 is deferred to S5 and tier 1 to S6") and S5 runs it one step later, and the unedited gate genuinely passes in between (verified above). The tier-1 half of S4's stated *reason* is the already-twice-refuted `TestValidTransitions_spec_6_2` claim, so filing on it is a close variant of a refuted finding.

MISTAKE (nearly filed): "S10's `Depends on: S9` omits S2 and S3, although CODE-5's `ExcludePods` implements SPEC-2's §5.2 `**Max retries:**` sentence and its third accounting caller implements SPEC-3's leak-accounting clause." S9 names S2 and S3, and the checklist executes as one ordered sequence, so both are ticked before S10 either way. The proposal's own convention lists dependencies non-reduced (S9 names S7 although S8 already does), so this is a convention inconsistency rather than an unmet dependency.

MISTAKE (nearly filed): "`slotCleanupBudget(cleanupTimeoutSeconds, maxConcurrentSessions)` divides by a field that can be zero." Both call paths normalise: `bindConcurrentSlot` is reached only under `match.MaxConcurrentSessions > 1` (start.go:2139) and `resumeOnPod` passes `maxConcurrentSessions(match.MaxConcurrentSessions)`. It is ordinary implementation judgment, not an underspecified target.

USEFUL [Traps, "The §5.2 append can silently redirect four tier-11 gates"]: it named the exact mechanism (`lineContaining` returns the FIRST match, and :453 precedes every bullet) and the exact thing stopping it, which turned a half-hour gate audit into two greps.

USEFUL [Traps, "Do not read `pkg/sandbox/slotstate` as adapter-side" and the `TestValidTransitions_spec_6_2` entry]: together they pre-killed the S4/S6 split candidate before I spent a round on it.

FACT: `binder_test.go` is still in no file list (standing Open), and I could not turn it into a finding: the resume-path tier-1 case only READS `fakeAssigner.released` and `concurrentAdapter` lives in `slotbinder_test.go`, so no edit to `binder_test.go` is actually required and same-package visibility covers the rest. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:71 (`concurrentAdapter`), binder_test.go:301,:322 (`fakeAssigner.released`).


### [non-spec.1.review-citations.1]

DECISION: returned an EMPTY findings list — BECAUSE every concrete citation I opened resolved exactly, including the newest (fix-stage) text, and the two candidates I built both reduce to the wording/precision class this loop has refuted repeatedly — ALTERNATIVES: filing the `summary.md:259` staleness (see DEFERRED below) and the "no code reads the reason value" over-statement (`non-spec-changes.md:442`, contradicted only by `pkg/adapter/session.go:260` which the preceding sentence already names as the normaliser); both change no staged code, no test, and no applied spec sentence.

USEFUL [Settled #254 "The whole-staging citation sweep is COMPLETE and clean"]: it is accurate. I re-opened roughly seventy citations across `pkg/adapter`, `pkg/gateway/podlifecycle/{podsession,podclaim}`, `pkg/gateway/sessionserver`, `pkg/sandbox/slotstate`, `cmd/lenny-gateway`, `cmd/lenny-adapter`, `schemas/`, `spec/04,05,06,28`, `docs/reference/state-machines.md`, and the tier-4/7a/11 test files, and every one landed. A future citation lens should sample rather than re-sweep.

FACT: the newest text in the staging is the `budget/2` graceful-window split (CODE-4), and its citations are all exact. `userTerminateRPCTimeout` is 20s and `userTerminateDeadline` 10s, so the "§11.4 holds the same relation" claim is literally a 2:1 ratio — EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55,:128-129
FACT: `spec/28_communication-channels.md:1082` is the `terminate` frame row and `schemas/runtime-ops-events.schema.json:180-183` carries `deadlineMs` `minimum: 100` plus the `required` list. Both cited claims hold, and the 5s budget floor puts `budget/2` at 2500ms — EVIDENCE: spec/28_communication-channels.md:1082; schemas/runtime-ops-events.schema.json:180-183
FACT: `p.addActiveLocked` is at socketruntime.go:184 and :220 exactly, and `return p.listener.Close()` at :467. Hand-counting from a `sed` window is off by one here because of a wrapped comment; use `grep -n` for these — EVIDENCE: pkg/adapter/socketruntime.go:184,:220,:467
FACT: `resume.go`'s release branches are :69,:73,:89,:107,:126,:134,:141 with `Runtime.Start` at :140 and `noteRuntimeStarted` at :144. `claimSessionSlot` is at :50. The summary and CODE-2 both quote this set correctly — EVIDENCE: pkg/adapter/resume.go:50,140,144
FACT: the ten `noteRuntimeStarted` call sites CODE-2 enumerates are exact and exhaustive; `grep -n noteRuntimeStarted pkg/adapter/` returns precisely them — EVIDENCE: pkg/adapter/{session.go:163,resume.go:144,sdkwarm.go:261,export_test.go:45,usage_test.go:233,podmcp_arming_internal_test.go:84/185/230,adapterevents_test.go:95/184}
FACT: the mechanical anchor sweep still passes on the current staging. A 10-line python script over the 28 fenced blocks of `.spec-changes.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24 and 0 for every other block. No anchor has drifted since the last run — EVIDENCE: proposals/0081_.../0081_....spec-changes.md fenced blocks vs spec/*.md

DEFERRED [proposals/0081_.../0081_....summary.md, open-decision 9 at :259]: it reads "the reclaim's §5.2 cleanup budget bounds the gateway's wait rather than the adapter's teardown". That was true when `compensateFailedSlotBind` sent `0` as the fourth `Shutdown` argument. The `budget/2` split now pins half the budget as the adapter's own graceful window, which the adapter spends inside `contextWithGraceDeadline` around `Runtime.Close` and sends on the `terminate` frame, so the budget does bound part of the adapter's teardown. What is still true, and is the half decision 9 rests on, is that nothing bounds `removeSlotTree`: it runs after the close, outside `s.mu`, and the handler performs no context-expiry check, so a lagging reclaim can still be deleting the tree when a retry lands. Repair the clause by naming the tree removal rather than "the adapter's teardown". Not filed: the decision's conclusion is unaffected and the sentence lands in no spec file. EVIDENCE: pkg/adapter/session.go:262-264,:271; proposals/0081_.../0081_....non-spec-changes.md:419

WATCHOUT: do not file `non-spec-changes.md:442` ("No code under `pkg/`, `cmd/`, or `sdks/` reads the reason value") as a false claim. `pkg/adapter/session.go:260` does read it and `pkg/gateway/runtime/adapterclient/client_test.go:707` asserts on it, but the sentence two lines earlier already names `drainReason` as the normaliser, so the intended reading is "nothing branches on it". `grep -rn "GetReason()" pkg/ cmd/ sdks/` returns exactly three hits and settles it in one command — EVIDENCE: pkg/adapter/session.go:260; pkg/gateway/runtime/adapterclient/client_test.go:707

WATCHOUT: `pkg/gateway/podlifecycle/podsession/binder_test.go`'s `fakeAssigner.released` field is at :302 and its `ReleaseSession` method at :322, while the proposal cites ":301,:322". :301 is the field's doc comment. Off-by-one on a comment line, meaning unchanged; do not file it — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:301-302,:322

OPEN: the summary's Decisions bullet at :93-96 still says "the adapter edits land in `session.go` and `runtimegeneration.go`" while `## Files touched on application (non-spec)` and the 0080 §1.1 impacts row both name `resume.go` and `sdkwarm.go` as well. This is the standing bookkeeping Open seen a third time. It costs an implementor nothing (the files list is complete) and the class has been refuted five times on materiality, but a fixer touching either site must sweep all three.


### [non-spec.1.review-client-surface.1]

DECISION: returned an empty findings list — BECAUSE every externally-consumed surface this
proposal touches has a verified, complete parallel representation, and the three that are
incomplete (the proto `Shutdown`/`ReportSessionScrub` comments, `docs/reference/adapter-contract.md:75`,
`docs/api/internal.md`'s adapter block) are already carried as Deferred entries or refuted
findings — ALTERNATIVES: rejected filing the `docs/api/internal.md` gRPC-status-table gap
(see FACT below), the `terminate` reason-value question (Open item, no representation
mismatch), and the resume-path gRPC-code-classification gloss (see FACT below).

FACT: `Client.Shutdown`'s fourth parameter is a `time.Duration`, and `cl.Shutdown(..., budget/2)`
type-checks; the value reaches the runtime as `int32(deadline.Milliseconds())` → the adapter's
`req.GetDeadlineMs()` → `s.drainViaLifecycle(...)` and `contextWithGraceDeadline(...)`. The
`budget/2 >= 2500ms` claim holds because `slotCleanupBudget` floors at 5s, so the CH-RUNTIMEOPS
`terminate` frame's `"minimum": 100` on `deadlineMs` is satisfied on every pool configuration.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824; pkg/adapter/session.go:260,:262;
schemas/runtime-ops-events.schema.json:180-183.

FACT: every citation in CODE-4's `terminate`-frame paragraph (non-spec-changes.md:429-446)
resolves exactly. `drainReason` is pkg/adapter/session.go:309-321 and is the ONLY reader of
`ShutdownRequest.reason` in the whole tree (`grep -rn "req.GetReason()" pkg/adapter/` returns one
hit, session.go:260). The §11.4 precedent is `const userRevokeReason = "USER_REVOKED"` at
cmd/lenny-gateway/user_revocation.go:45 passed at :129. `spec/28_communication-channels.md:1082`
is the `terminate` row and `schemas/runtime-ops-events.schema.json:181` is its reason enum.
Do not re-verify this paragraph.

FACT: the runtime SDKs never branch on the `terminate` frame's `reason` — all three pass it
through as an opaque string to an `invokeTerminate(reason, deadlineMs)` host hook. So a new
`ShutdownRequest.reason` value costs no SDK edit in any language, whatever the normalizer does.
EVIDENCE: sdks/runtime/go/runtime/lifecycle.go:198,:224,:293;
sdks/runtime/python/lenny_runtime/lifecycle.py:249,:267; sdks/runtime/typescript/src/lifecycle.ts:249-264.

FACT: the JSONL `shutdown` frame's `reason` is a free-form `{"type":"string","minLength":1}` with
the enum values named only in the description, so nothing on the Basic/Standard carrier constrains
a reason value either. EVIDENCE: schemas/lenny-adapter-jsonl.schema.json:109-118.

FACT: `SLOT_FAILED` is a client-visible 422 code emitted at pkg/gateway/sessionserver/start.go:313
and pinned by tests/tier3_contract/rest_sessions/slot_address_absence_test.go:369, and it appears
in NO row of `docs/reference/error-catalog.md`, `docs/api/*`, `docs/client-guide/*`, or
`pkg/gateway/externalapi/openapi/openapi.json`. Pre-existing and outside 0081, which mints no new
code, category, or `details.reason`. Recorded so the next client-surface lens does not spend a
round re-deriving it as a missing-edit-site candidate.

FACT: `openapi.json` carries no error-code enumeration at all (grep for `WARM_POOL_EXHAUSTED`,
`STARTING_FAILED`, `no_idle_pods` returns nothing across its 4301 lines), so no gateway error-path
change can ever be an OpenAPI edit site. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json.

WATCHOUT: `docs/api/internal.md:490-499` holds a "gRPC status codes / When used" table for the
adapter surface, and `ABORTED` is not a row in it, so CODE-2's `codes.Aborted` looks like a
missed client-facing edit site. It is not filable: `pkg/adapter/checkpoint.go:115` and
`pkg/adapter/oplock.go:36-40,:82` already return `Aborted` today, so the table is already
incomplete against the shipped tree, and the same page describes the adapter RPCs under the
retired `StopSession`/`clean_exit` names (Settled entry on docs/api/internal.md:95-160). Same
pre-existing-enumeration-gap class as the refuted `concurrent_slots_exhausted` and
`troubleshooting.md` candidates. EVIDENCE: docs/api/internal.md:499; pkg/adapter/oplock.go:36-40.

WATCHOUT: CODE-2's `codes.Aborted` justification (non-spec-changes.md:289-311) is entirely about
the BIND path — `SlotBindError.Reason()`, `NonRetryable()`, `applySlotRetryPolicy`,
`classifySlotBindFailure` — and the `Resume` arm then says it answers `Aborted` "for the
classification reason the `StartSession` rollback records above" (:322-325). That reason does not
obtain on the resume path: `resumeOnPod` returns the error to `isTransientPodClaimError`
(start.go:3648-3681), which matches only `*PoolWarmingError`, `*CredentialAssignmentError`,
`*SetupCommandFailure` and five sentinels and never inspects a gRPC code, and `accountSlotFailure`
takes the discriminator as a parameter rather than calling `Reason()` (the `reason := sbe.Reason()`
line is in `applySlotRetryPolicy`'s own body at start.go:2828, outside the extracted tail). I did
NOT file it: on the resume path the compensation runs only in `Binder.Resume`'s FAILURE branch, so
`cl.Resume` has already returned and the handler's `Aborted` reaches a client that has given up;
a second concurrent `Resume` for the same session on the same pod is unreachable because §7.3
claims from idle inventory through `podclaim.Claimer.Claim` and never re-enters a claimed pod. So
the gloss is inaccurate but the code choice is inert there. If a later round wants it, the fix is
one clause ("and because the resume path does not classify on the code at all"), not a code change.

FACT: `docs/reference/state-machines.md`'s per-slot table (:233-237) is the ONLY reader-facing
mirror of §6.2's per-slot fence, confirmed by grepping the whole of `docs/`, `sdks/`, `schemas/`,
`charts/`, `pkg/embedded/` and `pkg/gateway/externalapi/` for `receiving_uploads` and
`slot_cleanup`. The `receiving_uploads` hits in docs/runtime-author-guide/lifecycle.md:39 and the
two warm-path SVGs are the POD-level machine. DOCS-1's single row is the complete mirror
obligation and there is no third representation. Do not re-run this sweep.

FACT: `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311` is the gate on
`docs/reference/adapter-contract.md`'s `Shutdown` row, and it asserts only the four substrings
"end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub", plus the
absence of a `Terminate` row. All five survive SPEC-1 verbatim, so the stale :75 row passes
silently. This is the shipped confirmation of the standing Deferred; a lens hoping the gate will
catch it should stop here.

USEFUL [Settled: "No per-slot sub-state is client-visible"] and [Settled: "The per-slot vocabulary
has no SDK, CRD or OpenAPI mirror"]: these two entries plus the note that the OpenAPI document
lives at `pkg/gateway/externalapi/openapi/openapi.json` rather than the `pkg/gateway/openapi/` path
the lens brief gives saved this pass most of a round. The brief's path is wrong; the file is under
`externalapi/`.


### [non-spec.1.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the docs-alignment lens — BECAUSE every docs/ site the
staged behaviour touches is either edited (DOCS-1), already adjudicated (the `adapter-contract.md:75`
DEFERRED, refuted on materiality), or verified unchanged. ALTERNATIVES rejected, each with the ground
that killed it: (a) re-filing `docs/reference/adapter-contract.md:75` — eleven lenses have filed it and
the material skeptic refuted it as incomplete-about-a-new-path rather than false-about-the-contract-it-
documents; (b) `docs/runbooks/warm-pool-exhaustion.md` gaining the excluded-pod `WARM_POOL_EXHAUSTED`
cause — its Diagnosis steps enumerate replenishment/admission/quota/node-pressure causes, not placement
disqualification, so it is not the page that owns the cause, and the sibling `troubleshooting.md`
candidate is already a standing Trap; (c) `docs/runbooks/pod-kill-during-session.md` gaining the new
"a failed re-attach now drains the replacement pod" consequence — that runbook diagnoses a pod kill and
enumerates no pod-drain causes at all, so the addition would be new material rather than a correction
of superseded text.

FACT: DOCS-1's whole docs footprint is verifiable in one pass and it is correct end to end.
`docs/reference/state-machines.md`'s per-slot table is four rows at :234-237 under `### Per-slot
sub-states` (:231 intro), DOCS-1's row lands between :235 and :236, and the trigger text is
byte-identical to SPEC-4's fence annotation. The `slot_cleanup → released` gloss at :237 and the
"Two further per-slot edges" sentence at :251 both survive unchanged (still exactly two scoped edges).
EVIDENCE: docs/reference/state-machines.md:228-251.

FACT: the staged tier-11 extension is mechanically sound against the shipped file. `generalSlotEdges`
is a four-string slice at tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37;
the positive loop over `generalBlock` is at :55, the negative loop over `scopedBlock` at :70, and the
docs `requireAllContain` list is at :102-109. The proposal cites `:32-36` and `:102-108`; both sit
inside the real spans and neither is a false citation. The paired substring DOCS-1's row must satisfy,
`` `receiving_uploads` | `slot_cleanup` ``, matches the row verbatim because the shipped rows are
spelled `` | `receiving_uploads` | `running` | ``.

FACT: Trap 369's first-match hazard was re-checked against the CURRENT SPEC-3 append and is clear, and
there is one more anchor worth knowing about. The append contains the literal string `**Slot cleanup:**`
("That per-slot cleanup is the one the **Slot cleanup:** bullet below states"), and it lands at
spec/05:453, ninety lines ABOVE the real bullet at :545. A `lineContaining(s52, "Slot cleanup")` gate
would therefore be redirected to the scrub-model paragraph. No such gate exists today: `grep -rn "Slot
cleanup" tests/ scripts/ cmd/` returns only doc-comment prose in
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go and
session_scrub_report_addressing_doc_reconciliation_test.go, neither of which anchors on it. A future
round adding a §5.2 slot-cleanup gate must anchor below the append.

FACT: the four §5.2/§6.2-adjacent tier-11 gates in
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go were re-derived clean against
the current staging. They anchor on the capitalised "Whole-pod replacement trigger" (:92), "Session
count limit" (:155), "Uptime limit" (:298) and "increments `lenny_gateway_pod_retirement_total`" (:222);
SPEC-3's append writes "whole-pod replacement trigger stated below" and "the pod's served-session count"
in lower case and carries none of the other three. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
(basic_level_echo_stamp_doc_reconciliation_test.go:294-311) reads only docs/reference/adapter-contract.md,
never the spec §4.7 row, so SPEC-1's row rewrite cannot reach it.

FACT: the new-identifier sweep is still empty after the round's edits. `grep -rn
"ExcludePod|slot_bind_failed|runtimeHolds|slotCleanupBudget|accountSlotFailure|materializeSlotStages|
compensateFailedSlotBind" spec/ docs/ schemas/ charts/` returns zero hits.

FACT: this round's whole delta is two paragraphs and neither touches docs. `diff -rq
scratchpad/cp-snap/0081/non-spec-recheck-2-r5 proposals/0081_...` differs only in summary.md (one clause
appended to the 0078 impacts row: "it edits no file 0078's TEST-7 rewrites") and problem-statement.md
(the 0078 sequencing paragraph rewritten to withdraw the widened-exposure premise). Do not spend a round
hunting a docs-relevant fix-stage delta here.

WATCHOUT: `budget/2` as the fourth argument to `cl.Shutdown` is type-correct and does NOT mint a wire
problem, so do not file it. `Client.Shutdown` takes `deadline time.Duration` and converts with
`int32(deadline.Milliseconds())`, so the 5s budget floor yields 2500ms, above the
`runtime-ops-events.schema.json` minimum of 100. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:817.

USEFUL [Traps 335, 375, 376, 378, 302]: the standing Traps on `adapter-contract.md:81`,
`security-principles.md:33`, `execution-modes.md:68`, `multi-tenancy.md:72`,
`runtime-author-guide/lifecycle.md:319,:330`, `troubleshooting.md:36-44` and the
`slot_cleanup ──→ released` annotation saved this lens roughly the whole round. Every one of those is a
site a corpus grep lands on first, and each already has its refutation recorded.


### [non-spec.1.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every edit-site candidate I could
construct either resolved clean against the tree or is already on the refuted/DEFERRED list
— ALTERNATIVES: re-filing `docs/reference/adapter-contract.md:75` (refuted by the material
skeptic), the `slot_cleanup ──→ released` three-action gloss (standing trap bars it), the
tier-7a `tests/spec-map.json` row (refuted), the `podclaim` tier-2 label (declined twice).

FACT: `spec/18` needs no edit, and this closes the standing UNVERIFIED "Does `spec/18` need
an edit?". `grep -n "slot cleanup\|per-session slot\|whole-pod scrub\|slot release\|
ReportSessionScrub\|Shutdown" spec/18_*.md` returns exactly one line, the phase-12c
deliverable bullet, which names "the per-session slot cleanup and whole-pod scrub split"
without enumerating any precondition, so SPEC-1's two-teardown split and SPEC-3's withheld
report leave it true verbatim. EVIDENCE: spec/18_phased-build-sequence.md:531.

FACT: `validate-maps`' file-level rule is confirmed in code, so the standing correction is
right and a new test FUNCTION in a listed file is genuinely free. `validateTestFilesMapped`
normalises every spec-map entry by stripping `::TestName` and `/...` and then matches the
walked `_test.go` path (or any ancestor directory) against that set; a function name is never
compared. `tests/tier4_integration/concurrent_workspace_test.go` is mapped as a bare file
path and `recycle_scrub_path_test.go` only as `::Test...` entries, and BOTH pass, because the
`::` prefix is stripped to the file path. EVIDENCE: cmd/lenny-test/cmd_validate.go:733-741,
:768-776; tests/spec-map.json:552,:1040.

FACT: the three tier-11 literal sweeps cannot be tripped by SPEC-3's widened action list.
The retired credential literal is built as `"/run/lenny/" + "credentials.json"`, and SPEC-3
writes the directory and the filename as two separate backticked tokens
(`/run/lenny/slots/{sessionId}/` and `credentials.json`), so the concatenated substring never
appears. The retired slot placeholders are `{slotId}` / `<slotId>`; SPEC-3 uses `{sessionId}`
and leaves the bare identifier `slotId` (no braces) in the surviving clause, which the sweep
does not match. EVIDENCE: tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go:40;
tests/tier11_docs/slot_placeholder_literal_sweep_test.go:36-42.

FACT: no tier-11 `requireLine`/`lineContaining` anchor over §5.2 collides with SPEC-3's
scrub-model append, checked string by string. The nine anchors are "Whole-pod replacement
trigger", "Session count limit", "increments `<gateway metric>`", "Uptime limit", "The
gateway triggers the whole-pod scrub", "**Slot (session mode).**", "A service-mode slot is a
different thing", "**Fresh-guest reprovision:**" and "the pod is held for its tenant through
the claim's `reserved` state". The append's only near-miss is "whole-pod replacement trigger
stated below", which differs in case from the anchor and so is not a first-match hijack.
EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155,:222,:298;
recycle_scrub_trigger_consistency_test.go:70,:144; slot_definition_glossary_reconciliation_test.go:84,:127;
vm_restart_reprovision_consistency_test.go:71,:197.

FACT: every `podsession.SlotBindRequest` and `podclaim.SlotRequest` composite literal in the
tree is KEYED, so adding `ExcludePods` breaks no build. Twenty-odd literals across
slotbinder_test.go, workspace_base_propagation_test.go, slotclaimer_test.go, binder.go:1685,
slotbinder.go:423, start.go:2541, slotretry_test.go:70 and
start_preclaim_internal_test.go:1021, all field-named. EVIDENCE: `grep -rn "SlotRequest{\|SlotBindRequest{" pkg/ tests/`.

FACT: there is a NINTH `classifySlotBindFailure` call site the standing "twelve plus four"
inventory does not name — `pkg/gateway/sessionserver/start_preclaim_internal_test.go:1019`.
It is NOT a defect: it passes a keyed `podsession.SlotBindRequest` literal by value and
`classifySlotBindFailure` keeps its by-value signature under CODE-5, so it compiles unchanged
and the file owes no edit-list entry. A future lens counting that inventory will find the
extra site; do not file it. EVIDENCE: pkg/gateway/sessionserver/start_preclaim_internal_test.go:1018-1022;
pkg/gateway/sessionserver/start.go:2761.

FACT: the imports CODE-1/CODE-2/CODE-4 need already exist at every target file, so no import
churn is an unlisted edit. `pkg/adapter/session.go` already imports `codes`, `status`,
`time` and `pkg/observability/tracing`; `pkg/adapter/resume.go` already imports `codes` and
`status`; `pkg/gateway/podlifecycle/podsession/slotbinder.go` already imports `context`,
`errors`, `log` and `time`. EVIDENCE: pkg/adapter/session.go:5-17; pkg/adapter/resume.go:5-17;
pkg/gateway/podlifecycle/podsession/slotbinder.go:5-19.

FACT: `adapterclient.Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)
(bool, error)` matches CODE-4's staged four-argument call exactly, and it sends
`Recycle: nil`, so the compensating reclaim cannot trigger the whole-pod scrub. It also does
`DeadlineMs: int32(deadline.Milliseconds())`, which makes the staged "half the budget is
≥ 2500ms, above the schema's minimum 100" arithmetic correct. EVIDENCE:
pkg/gateway/runtime/adapterclient/client.go:807-824.

FACT: `docs/reference/state-machines.md`'s per-slot table is a three-column
`| From | To | Trigger |` table and DOCS-1's staged row matches it cell for cell; the sibling
tier-11 assertion `` `receiving_uploads` | `slot_cleanup` `` is a literal substring of that
row. The concurrent-occupancy section's own two per-slot edges are stated in PROSE
(`running -> failed`, `slot_cleanup -> leaked`) rather than in a table, which is why
`TestStateMachinesDocMirrorsThePerSlotSubStateScope`'s negative assertion on `slot_assigned`
is unaffected by the added row. EVIDENCE: docs/reference/state-machines.md:232-237,:250;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:104-122.

WATCHOUT: `spec/29_communication-scenarios.md` step 14 pushes `FINAL_USAGE_REPORT` "on a
session end", and CODE-1 moves `emitFinalUsage` from the `bound` branch into the `started`
branch. It is NOT an edit site, because §29.4's Preconditions paragraph scopes the whole
trace to a started session, but the grep for FINAL_USAGE_REPORT lands a fresh lens on
spec/28:441,:456,:471 and spec/08:425 as well, and all four are about the delegation
lifecycle stream's ordering rather than about which sessions get one.
EVIDENCE: spec/29_communication-scenarios.md:712-716; spec/28_communication-channels.md:441,:456;
spec/08_recursive-delegation.md:425.

USEFUL [standing context, "The new-identifier sweep is complete and empty"]: re-running
`grep -rn "ExcludePod\|slot_bind_failed\|runtimeHolds\|slotCleanupBudget\|accountSlotFailure\|
materializeSlotStages\|compensateFailedSlotBind" spec/ docs/ schemas/ charts/` still returns
zero, and the singular `ExcludePod` pattern covers the plural `ExcludePods` the design
settled on, so the entry did not go stale when the field became a slice.

USEFUL [standing context, "`receiving_uploads` as a PER-SLOT state has exactly two spec sites
and one docs site"]: re-derived independently and it holds. The other hits
(state-machines.md:149-151,:175-176, runtime-author-guide/lifecycle.md:39, spec/15:672, the
two warm-path SVGs) are all the SESSION-model machine. This is the single most useful entry
for an edit-site lens on SPEC-4/DOCS-1 and it saved a full sweep.


### [non-spec.1.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor-action assignment in the
staged non-spec lane was traced to a real symbol with the data its check needs, and the two
residual feasibility gaps I found are both already-refuted close variants (the
`compensateFailedSlotBind(req SlotBindRequest)` / `ResumeRequest` mismatch, and the
"tier N cannot read adapter-internal state" family). ALTERNATIVES: filing the
`start_test.go` package-visibility item (see FACT below) — rejected as the same
test-mechanics class the material skeptic already refuted twice.

FACT: **`pkg/gateway/sessionserver/start_test.go` is `package sessionserver_test`
(EXTERNAL), while `slotretry_test.go` and `slotretry_load_test.go` are `package
sessionserver` (INTERNAL).** The staged resume-accounting case has to reach `resumeOnPod`,
which is unexported, so it cannot live in `start_test.go` as the Testing section's file list
suggests; it lands in an internal file (`slotretry_test.go`, or a new
`*_internal_test.go`). It IS reachable from an external file through the HTTP resume
handler the way `start_pod_test.go` does, and the leak GAUGE is injectable
(`opts.SlotLeakGauge`), but `s.slotStates` is NOT: `sessionserver.go:1873` constructs
`slotstate.NewRegistry()` unconditionally, with no `Options` field, so an external test
cannot observe `MarkLeaked` directly and must discriminate through the gauge callback.
Not filed: implementation-time mechanics on files already in the edit list. EVIDENCE:
pkg/gateway/sessionserver/start_test.go:3; slotretry_test.go:3; sessionserver.go:1872-1875;
start_pod_test.go:3 (also external, and its :1420 mentions `resumeOnPod` only in a comment —
NO shipped test calls `resumeOnPod` directly today, which corrects the natural reading of
the standing Settled entry that cites `start_pod_test.go:203,:1420` as a resume driver).

FACT: the whole non-spec code lane re-verified as feasible, symbol by symbol, this round;
do not re-derive. `SlotClaimer.ReleaseSlot(ctx, sandboxName string, recycle, leaked bool)`
(slotclaimer.go:818) — note the parameter ORDER is recycle THEN leaked, so
`ReleaseSlotReservation`'s threaded disposition is the FOURTH argument;
`adapterclient.Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)`
(client.go:807) matches CODE-4's `budget/2` call verbatim; `Binder.BindSlot` has exactly ONE
production caller (start.go:2810) so the wrapper covers every release site;
`releaseResumeSlot`/`reserveResumeSlot` have no caller outside binder.go;
`createClaimNeedsRollback` is called only at start.go:833 and :1026 and `resumeOnPod` only at
treerecovery.go:29 and start.go:3494, so CODE-4's new `*SlotBindError` in the resume chain
reaches no create-path predicate; `bindConcurrentSlot` is a `*Server` method whose `slotReq`
is a value PARAMETER, so `&slotReq` outlives every `runWithQueue` re-entry;
`queue_internal_test.go` is `package sessionserver`, so the staged queue-re-entry case needs
no new file list entry. EVIDENCE: podclaim/slotclaimer.go:818; adapterclient/client.go:807;
sessionserver/start.go:2810,:833,:1026,:2594,:3494,:3943; podsession/binder.go:1710-1717;
sessionserver/queue_internal_test.go:3; treerecovery.go:29.

FACT: `deregisterSlotLocked` returns a NIL `*slotState` when `removed` is false
(slotsession.go:174-189), so CODE-1's `started := removed && st.started` and
`live := removed && s.runtimeHoldsLocked(sessionID)` are safe only because Go's `&&`
short-circuits. A fixer who reorders those conjuncts introduces a nil dereference.
EVIDENCE: pkg/adapter/slotsession.go:174-189; non-spec-changes.md:99,:106.

FACT: `spanErr` IS in scope at the `StartSession` rollback site — it is declared at
session.go:83 with a deferred `tracing.RecordError`, and `noteRuntimeStarted` is the last
statement before the success return at :163-164. `Server.Resume` genuinely opens no span
(resume.go:25-35 has no `tracing.NewTracer`), so the asymmetry CODE-2 records is real.
EVIDENCE: pkg/adapter/session.go:82-87,:163; pkg/adapter/resume.go:25-35,:144.

FACT: the four wire/enum citations in CODE-4's reason-string paragraph all resolve exactly.
`schemas/runtime-ops-events.schema.json` has `deadlineMs` `minimum: 100` at :180 inside the
`terminate` block spanning :174-185, and the `reason` enum at :181; `spec/28:1082` is the
`terminate` row of the CH-RUNTIMEOPS frame table; `drainReason`'s four-value switch is at
session.go:314-321 with the doc comment from :308; `cmd/lenny-gateway/user_revocation.go`
really does hold `userTerminateRPCTimeout = 20s` (:55) bounding a call that sends
`userTerminateDeadline = 10s` (:50) as the window, so the "same relation" claim holds.
EVIDENCE: schemas/runtime-ops-events.schema.json:174-185; spec/28_communication-channels.md:1082;
pkg/adapter/session.go:308-321; cmd/lenny-gateway/user_revocation.go:45,:50,:55,:129.

FACT: `spec/18` needs no edit and the question can be closed. Grepping spec/18 for slot,
scrub and recycle returns exactly one line, :531, a phase-12c deliverable naming "the
per-session slot cleanup and whole-pod scrub split" with no preconditions enumerated, so
nothing there goes stale and no deliverable of this proposal depends on a later-phase
artifact. This closes the standing Open "Does `spec/18` need an edit?".
EVIDENCE: spec/18_phased-build-sequence.md:531.

FACT: no §4.6.3 / §13.2 / §10.3 / webhook-purity boundary is crossed by the non-spec lane.
The only cluster writes the change adds callers to are `DrainSandbox` (an annotation stamp
the WarmPoolController acts on) and `SlotClaimer.ReleaseSlot`'s claim DELETE/patch, both
gateway-owned and both shipped; `ClaimSlot`'s new skip is a `continue` beside
`expiredByUptime`, whose own envtest tests already assert the gateway leaves
`Sandbox.status.phase` untouched. The adapter reads only in-process state
(`st.started`, `runtimeLive`) and issues no apiserver call. EVIDENCE:
podclaim/slotclaimer.go:416-441,:483-500,:818-885; podsession/slotbinder.go:601.

USEFUL [Standing context, Settled #180 "Every code target in the non-spec staging exists
with the stated signature"]: it is accurate and it saved this lane most of a pass. The one
correction it wants is the `start_test.go` package-visibility FACT above.

USEFUL [Standing context, Traps on the bound/started drift and on `runtimeLive`'s writers]:
I swept every "has been given" / "start the adapter has admitted" occurrence across the four
non-review-log files this round and all nine sites are on the correct side of the boundary.
That sweep is now clean; re-run it only if a fixer adds a sentence naming either predicate.


### [non-spec.1.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every claim I could reach in the tree checked out, and the three candidates I built each collapsed on evidence (below) — ALTERNATIVES: filing the `binder_test.go`-not-in-the-file-list gap and the "nothing pins `connectSlot`'s `ExcludePods` pass-through" test gap; both are close variants of already-refuted findings and both are recorded as standing Opens.

FACT: the round-5→now diff is TWO paragraphs and neither is staged text. `diff -ru scratchpad/cp-snap/0081/non-spec-recheck-2-r5 proposals/0081_.../` returns only the problem statement's 0078 paragraph (:105-111) and the summary's 0078 impacts row (:466). `spec-changes.md`, `non-spec-changes.md` and the checklist are byte-identical to r5. Do not spend a round hunting a fix-stage delta in the staging.

FACT: `TestResumeReleasesItsReservationWhenTheAdapterResumeFails_spec_5_2` SURVIVES CODE-4, and here is why. The standing WATCHOUT flags it as unnamed and exactly on CODE-4's branch. I traced it: the fixture leaves `srv.WorkspaceBase` empty, so the adapter refuses at `pkg/adapter/resume.go:33` BEFORE `claimSessionSlot` at :50, so the adapter holds no entry when the compensating `Shutdown` arrives; CODE-1's handler then has `removed=false`, `started=false`, `live=false`, `treeErr=nil`, `closeErr=nil` and answers `ExitedCleanly: true`. `compensateFailedSlotBind` returns false, `releaseResumeSlot(..., leaked=false)` decrements as today, and both subtests' `active_slots` and claim-disposition assertions hold. EVIDENCE: pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go:211-260; pkg/adapter/resume.go:33,:50; pkg/adapter/session.go:238-291.

FACT: `slotCleanupBudget` cannot divide by zero on any staged path. `SlotBindRequest.MaxConcurrentSessions` comes from `slotBindRequest` (start.go:2546), which is reached only under `match.MaxConcurrentSessions > 1`; `ResumeRequest.MaxConcurrentSessions` is normalized through `maxConcurrentSessions()` at start.go:4029 and `Binder.Resume` has exactly one production caller. EVIDENCE: pkg/gateway/sessionserver/start.go:3353-3358,:4005,:4029; pkg/gateway/podlifecycle/podsession/binder.go:640-645.

FACT: the grace split compiles and satisfies the frame schema. `Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)` takes a Duration, so `budget/2` is well-typed, and it converts with `int32(deadline.Milliseconds())`. On the adapter side `contextWithGraceDeadline(parent, grace)` is `context.WithTimeout`, so the close context is `min(budget, budget/2)` and the gateway genuinely outlasts it. The 5s budget floor puts `deadlineMs` at 2500 or above, over the schema's minimum of 100. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824; pkg/adapter/session.go:327-332.

FACT: the tier-7a park cannot deadlock against the reclaim's `Runtime.Close`. `gatedRuntime` holds FOUR maps — `startGate`/`startEntered` and `closeGate`/`closeEntered` — and `park(sessionID, onClose)` returns immediately when the matching `entered` channel is nil, so arming only the start gate leaves `Close` unparked. EVIDENCE: tests/tier7a_load_local/podmcp_arming_handoff_test.go:46-101.

FACT: the tier-11 `generalSlotEdges` addition is safe on BOTH loops, and the spec/07 half of that test does not read the edge list. `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` runs the positive loop over `generalBlock` (generalHeader to end of §6.2, so SPEC-4's new prose paragraph is inside it) and the negative loop over `scopedBlock` (scopedHeader to generalHeader); the spec/07 assertion is a separate `crossRef` line check that touches no edge string. So adding one entry does not oblige spec/07 to carry the edge. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:54-55,:64-71,:76-81.

FACT: every "text to replace" anchor still matches the tree byte for byte, re-verified this round by grep rather than by trusting the standing entry: spec/04:157, :686, :854; spec/05:453, :545, :555; spec/06:234 and the §6.2 general fence block at :150-156; spec/07:23, :213, :214, :414; spec/29:711 (the step-13 tail, whose "([§15.4.3](...), §28.5.3)." string also occurs at :645, :887 and :982 and resolves only through the "§29.4's numbered step 13" scoping).

FACT: all five production `ReleaseSlotReservation` call sites match CODE-4's table one for one — slotbinder.go:172 (inside `ClaimSlot`, the connect-stage release), slotbinder.go:217 (`BindReservedSlot`), binder.go:1714 (`releaseResumeSlot`), start.go:2834 (`applySlotRetryPolicy`), start.go:3246 (`rollbackClaim`) — plus the interface at start.go:2727 and the two fakes at slotretry_test.go:52 and slotretry_load_test.go:33. `s.podBinder` is the concrete `*podsession.Binder` (sessionserver.go:188), so the third `accountSlotFailure` caller satisfies the `slotBinder` interface with no new adapter type.

MISTAKE (nearly filed, and here is what kills it): "§7.1's obligation binds the creation finalize block but no staged code discharges it there." The finalize block engages the binder only on an EXCLUSIVE pool (`prepareAtFinalize` returns nil above concurrency 1), and the staged §7.1 block's own closing sentence scopes the reclaim off pods the failure terminates. This is the already-refuted exclusive-path family in a new dress; three of the refuted entries cover it.

MISTAKE (nearly filed): "`BindReservedSlot` cannot pass `sbe.Leaked` because its error may not be a `*SlotBindError`." The reconnect-stage errors at slotbinder.go:231-253 are all built through `b.slotBindError(...)`, so every error the release branch sees IS one; the only nil-`sbe` case would be a future non-wrapped error, which is ordinary implementor handling rather than a design gap.

USEFUL [Standing context → Traps]: the "Do NOT touch the 'A start that races the reclaim' bullet's closing sentence" and "MISTAKE, refuted at least eight times: §7.1 says the exclusive pod retires" traps each stopped a candidate I had half-written. The Settled entry "`SlotClaimer.ReleaseSlot(leaked=true)` returns early" stopped a third.

OPEN: nothing new. The three standing Opens I brushed against and did not advance are `binder_test.go` in no file list, `compensateFailedSlotBind`'s signature on the resume path, and `tests/spec-map.json` for the new tier-7a file.


### [non-spec.1.review-kubernetes.1]

DECISION: Returned an empty findings list for the Kubernetes-idiom lens — BECAUSE the staged delta since `non-spec-recheck-2-r5` touches only `.summary.md` (one 0078 impacts-row clause) and `.problem-statement.md` (the 0078 sequencing paragraph), neither of which asserts anything about a Kubernetes object, and my own independent re-derivation of every CRD-touching surface reproduced the previous kubernetes lens's empty result on new evidence. ALTERNATIVES: I built and killed two candidates that the previous kubernetes shard did NOT consider; both are recorded below so nobody rebuilds them.

FACT: the round delta is three files and none of them is a staging file. `diff -rq scratchpad/cp-snap/0081/non-spec-recheck-2-r5 proposals/0081_...` reports only `.problem-statement.md`, `.review-log.md` and `.summary.md`. `.spec-changes.md`, `.non-spec-changes.md` and `.implementation-checklist.md` are byte-identical. Do not spend a round hunting a fix-stage delta in the staging text.

MISTAKE (mine, withdrawn before filing): I nearly filed "the `leaked` release leaves a non-terminal per-pod `SandboxClaim` that holds the Sandbox's session-cleanup finalizer, so the pod wedges in Terminating and trips the §16.5 `FinalizerStuck` alert." Two things kill it. (1) The orphan GC does not merely drain: `reclaimByDraining` stamps the drain AND then `g.Client.Delete(ctx, claim)`, so the claim goes away and `activeClaimReferences` stops holding the finalizer. (2) `evaluate` gates on `PodHasActiveSession(claim.Spec.SandboxRef)`, so while a live co-tenant references the pod the finalizer is legitimately held; once the co-tenants end, the leaked slot's held occupancy keeps `ReleaseSlot` from deleting the claim but the pod has no active session, which is exactly the orphan-GC predicate. It is also pre-existing: `Binder.ReleaseSlot` already computes `leaked = err != nil || !cleanly` on every ordinary session end. EVIDENCE: pkg/controller/sandbox/finalizer.go:52-83,:108-125; pkg/controller/warmpool/gc.go:223-249,:347-365; pkg/gateway/podlifecycle/podsession/slotbinder.go:542.

MISTAKE (mine, withdrawn): I nearly filed "CODE-4's `leaked=true` on the resume path newly withholds the claim DELETE that retires the freshly claimed replacement pod, so a failed §7.3 re-attach strands a `claimed` pod that nothing reclaims." The withholding is real (`ReleaseSlot` early-returns at slotclaimer.go:830-836 before the counter decrement and before the occupancy-zero `DeleteClaim` at :881-885), but the pod is bounded twice over: CODE-5's third `accountSlotFailure` caller drains it at the §5.2 threshold, and below the threshold §4.6.1 orphan GC drains and deletes the `bound` claim after `claimOrphanTimeout` once no active session references the pod. `resumeOnPod` already leaves the replacement pod claimed with no rollback today (start.go:4041-4043), so the pod-claim half is pre-existing rather than staged. It is also the already-refuted "§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach" family.

FACT: the admission surface is CREATE-only and cannot be reached by anything staged. `sandboxclaim_guard.Decide` rejects any operation but `OpCreate` and applies per-pod uniqueness over non-terminal existing claims; it reads no phase transition, so neither the withheld DELETE nor the withheld `bound → recycling` patch can produce a rejected write. The staged change performs no `SandboxClaim` status write at all on any new path. EVIDENCE: pkg/admission/sandboxclaim_guard/guard.go:127-150.

FACT: `writeBoundStatus` really does run inside `reserveSlot`'s fresh-pod arm, immediately after `CreateClaim`, so the tier-4 datastore-crossing case's "the per-pod `SandboxClaim` survives at `bound`" is assertable rather than an empty-phase claim. A reviewer who assumes the CREATE-before-status window is the normal state will call that assertion false. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:689-712.

FACT: `ClaimSlot`'s two `expiredByUptime` skips, which the staged `ExcludePods` skip copies, sit at :433 (pass 1, after the per-pod claim GET and the tenant-pin check, BEFORE `rebindReservedSlot`) and :491 (pass 2, after the idle-phase check and the claim GET). Both carry an explicit "read-only placement filter; the gateway skips the pod without writing Sandbox.status" comment naming §4.6.3 ownership, which is the precedent the staged skip should copy verbatim. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:433-441,:491-498.

FACT (re-verified independently): `Binder.DrainSandbox` stamps `lenny.dev/drain-request` on the agent Pod and its own doc comment records that the gateway never writes `Sandbox.status`, the WarmPoolController being the sole writer per §4.6.3. CODE-5's two new `accountSlotFailure` callers reach that same already-blessed mechanism, so neither is a gateway write into controller-owned status. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:583-603.

USEFUL [Standing context → Settled, "Ownership is clean" and "The `SandboxClaim` a leaked reclaim leaves `bound` is not undeletable"]: both were accurate and saved a re-derivation, but neither mentions the Sandbox session-cleanup finalizer, which is the second-order consequence a Kubernetes lens reaches for first. The MISTAKE entry above closes that gap; a future lens should read it before rebuilding the finalizer argument.


### [non-spec.1.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every mechanism I traced end to end
(adapter `Shutdown` clause two under CODE-1, CODE-2's `noteRuntimeStarted` guard and both
rollback arms, CODE-4's `materializeSlot` wrapper plus the three reclaim entry points,
CODE-5's three `accountSlotFailure` callers and the `ExcludePods` filter, and each staged
spec sentence read against the code that implements it) either checks out or is already on
the refuted/fixed list — ALTERNATIVES: I built and dropped candidates on the exclusive-pool
`Binder.Prepare`/`Launch` gap, the `fenceResumedPod` unreclaimed abandon, `live && !started`
reachability, and the tier-4/tier-7a internal-state assertions; each is either an already
refuted family or explicitly recorded as out of scope.

FACT: the reclaim gate has exactly THREE production entry points and the staging covers all
three with no fourth. `Binder.BindSlot` (start.go:2810 via `applySlotRetryPolicy`),
`Binder.BindReservedSlot` (start.go:2596), and `Binder.Resume` (start.go:4005). A grep for
`.BindSlot(`/`BindReservedSlot(`/`podBinder.Resume(` over non-test `pkg/` and `cmd/` returns
those four lines and nothing else, and `bindReservedSlot` funnels into `materializeSlot` too
(slotbinder.go:254), so the one wrapper genuinely covers both bind paths. Do not re-derive
the "is a bind path missed" question. EVIDENCE: pkg/gateway/sessionserver/start.go:2596,:2810,:4005;
pkg/gateway/podlifecycle/podsession/slotbinder.go:129-134,:254.

FACT: `ReleaseSlotReservation` has exactly SIX production call sites and CODE-4's table names
all six with no omission and no phantom. `slotbinder.go:172` (ClaimSlot connect-stage),
`:217` (BindReservedSlot), `binder.go:1714` (releaseResumeSlot), `start.go:2727` (the
`slotBinder` interface declaration), `:2834` (applySlotRetryPolicy), `:3246` (rollbackClaim).
Plus the two test fakes at `slotretry_test.go:52` and `slotretry_load_test.go:33`, both in
"Files touched". EVIDENCE: `grep -n ReleaseSlotReservation -r pkg/ cmd/`.

FACT: CODE-1's staged clause two preserves the shipped ORDER exactly for a started session,
so the "credential file removed inside the §15.4.2 grace window" comment stays true. Shipped
`if bound {}` runs emitFinalUsage → drain → Close → noteRuntimeClosed → removeSlotTree →
cancelPodMCPIfRuntimeIdle → reportSessionScrub; staged runs `if started {emitFinalUsage,
drain, Close, noteRuntimeClosed}` then `if removed {removeSlotTree, cancelPodMCPIfRuntimeIdle}`
then `if live {reportSessionScrub}`, and `started ⊆ removed`, `live ⊆ removed`. A reviewer
tempted by an ordering finding should stop here. EVIDENCE: pkg/adapter/session.go:242-281;
non-spec-changes.md:91-139.

FACT: `live && !started` is unreachable, which is why CODE-1's three-predicate shape has no
"report without a close" arm. `runtimeLive` membership is written only by
`noteRuntimeStartedLocked`, which runs only after `claimSessionSlotUnderLock` set
`st.started`, and `st.started` is never cleared; the only way to get a present entry with
`started==false` for a session in `runtimeLive` is a delete-then-recreate, and every deleting
path either ran the `started` block (which calls `noteRuntimeClosed`) or ran pre-`Runtime.Start`
(so not live). Nobody has written this down; it is what makes the `live` report gate safe in
BOTH directions rather than only the one the standing context records. EVIDENCE:
pkg/adapter/runtimegeneration.go:36-49,:58-69; pkg/adapter/slotsession.go:80-88,:174-189.

FACT: the exhaustion outcome the staged §5.2 constraint promises is mechanically produced.
When the excluded pod is the pool's only member, pass 1 skips it, pass 2 skips it again (it is
`claimed`, not `Idle`), `len(list.Items) != 0` and `sawTenantMismatch` is false, so `ClaimSlot`
falls through to `ErrNoConcurrentSlot`, which `applySlotRetryPolicy` surfaces unwrapped and the
caller maps to `WARM_POOL_EXHAUSTED`/`concurrent_slots_exhausted`. Verified against the real
fallthrough rather than inferred. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:509-521;
pkg/gateway/sessionserver/start.go:2814-2819.

WATCHOUT: the `budget/2` graceful window is a `time.Duration` and `adapterclient.Client.Shutdown`
converts it with `int32(deadline.Milliseconds())`. A future edit that changes the floor below 200ms,
or that passes the budget in seconds, silently mints a `terminate` frame whose `deadlineMs` violates
`minimum: 100` in `schemas/runtime-ops-events.schema.json`. The 5s floor is what keeps half-budget
≥ 2500ms. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:813-818;
pkg/adapter/session.go:260-262; non-spec-changes.md:419,:439-441.

WATCHOUT: `st` is nil when `removed` is false (`st, removed = s.slots[sessionID]`), so CODE-1's
`started := removed && st.started` is only safe because Go short-circuits `&&`. An implementor
who "simplifies" to `started := st.started && removed`, or who hoists `st.started` into a local,
nil-derefs on the no-entry `Shutdown` the staged §4.7 no-op sentence makes the common case.
EVIDENCE: pkg/adapter/slotsession.go:174-176; non-spec-changes.md:99.

FACT: `bindConcurrentSlot`'s `slotReq` is a value PARAMETER, and `runWithQueue`'s closure captures
that parameter, so `&slotReq` is a pointer whose lifetime spans every queue re-entry within the
one call. This is the mechanical ground for CODE-5's pointer threading and I re-verified it
directly. EVIDENCE: pkg/gateway/sessionserver/start.go:2594,:2606-2609.

UNVERIFIED: the tier-4 first case asserts "the pod's registry holds only alice" from
`package tier4_integration_test`, which cannot read `s.slots`. It is observable only through
the second clause of the same sentence (the unaddressed-frame relay, which reads
`slotCount()` at pkg/adapter/attach.go:357). The same shape as the already-refuted tier-7a
`runtimeLive` item, so I did not file it, but an implementor should read the two clauses as
one assertion rather than two. EVIDENCE: non-spec-changes.md:954-957;
pkg/adapter/attach.go:345-364.


### [non-spec.1.review-operational.1]

DECISION: returned an EMPTY findings list — BECAUSE every surface this lens owns (alerts, metric
inventories, condition writers, operator-facing state docs, tier-11 spec/doc reconciliation gates)
was re-derived from the tree this round and each came back clean or already-adjudicated —
ALTERNATIVES: the four candidates I built and dropped are listed below with the evidence that
killed each, so a later operational pass does not rebuild them.

USEFUL [Standing context, Settled #224]: "No alert anywhere references any slot metric" saved the
whole alert half of this lens. Re-verified independently this round by listing every `Name:` /
`Expr:` in `pkg/alerting/rules/rules.go` and by `grep -rn slot docs/runbooks/`: the only runbook
hits are Redis hash slots, a Postgres replication slot, and the credential path. Nothing this
proposal stages can orphan an alert or a runbook. EVIDENCE: pkg/alerting/rules/rules.go:278-560;
docs/runbooks/ (grep).

FACT: `recordStartupDuration` has exactly two call sites and BOTH sit after an `if err != nil {
return }`, so `lenny_session_startup_duration_seconds` samples successful starts only. The
compensating `Shutdown`'s added wait (up to `slotCleanupBudget`, and up to the whole pool
`cleanupTimeoutSeconds` on an exclusive `Binder.Resume`) therefore cannot inflate the histogram
that `StartupLatencyBurnRate` / `StartupLatencyGVisorBurnRate` read. This closes the "the
compensation burns the startup SLO error budget" candidate mechanically rather than by the
weaker §16.5 wording argument in Standing #342. EVIDENCE: pkg/gateway/sessionserver/start.go:2401,
:2673; pkg/alerting/rules/slo.go:214-242.

FACT: the four §5.2 tier-11 first-match anchors survive SPEC-3's append, and the reason is exactly
case. `requireLine`/`lineContaining` is a case-SENSITIVE first-match scan
(tests/tier11_docs/backup_status_enum_test.go:48-55). SPEC-3 appends to `**Scrub model.**`
(spec/05:453), which precedes every anchored bullet: `Session count limit` (:488), `Uptime limit`
(:489), the retirement-logging line (:492), `Whole-pod replacement trigger` (:561). The appended
text writes "whole-pod replacement trigger stated below" and "the pod's served-session count" in
lower case and contains none of the other three anchor strings, so every gate still resolves to its
own bullet. I re-derived this from the staged text rather than trusting Standing #369; the
conclusion matches. A fix round that recases either phrase turns four tier-11 tests red by
retargeting them at line 453.

FACT: SPEC-4's `**Pre-`running` slot cleanup.**` paragraph is inserted between spec/06:157 and
:158, i.e. BEFORE the `**`leaked` slot semantics.**` paragraph at :160 that
`TestLeakedSlotCountingLifetimeAgrees_F5231` anchors on with `requireLine(s62, "`leaked` slot
semantics")`. It is safe only because the staged paragraph does not contain that exact phrase.
Verified against the staged block at spec-changes.md:515. Any later edit that adds the phrase to
that paragraph silently retargets the gate at the wrong line. EVIDENCE: spec/06_warm-pod-model.md:
156-160; tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:82-88.

FACT: the accounting chain SPEC-3's clause asserts ("a cleanup that does not complete ... is
surfaced on the `lenny_adapter_leaked_slots` gauge") closes end to end on all three bind paths
after CODE-5, and I walked it: CODE-1's `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`
→ `compensateFailedSlotBind`'s `err != nil || !cleanly` → `sbe.Leaked` → `accountSlotFailure`'s
`MarkLeaked` + `leakGauge` + `RecordLeak`. The gauge is registered and wired in production
(`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223`;
`cmd/lenny-gateway/sessionsrv.go`), and `resumeOnPod` is a `*Server` method with `s.slotHealth`,
`s.slotStates`, `s.slotReplacement`, `s.slotLeakGauge` and `match` all in scope
(pkg/gateway/sessionserver/start.go:3943, :4041; sessionserver.go:401-421). No gap.

FACT: spec/16 needs no edit and the two slot rows stay true, verified against the staged code
rather than by absence. `lenny_slot_failure_total` is emitted only by the five `recordSlotFailure`
calls inside `materializeSlot`, which CODE-4 leaves inside `materializeSlotStages`
(pkg/gateway/podlifecycle/podsession/slotbinder.go:287,:294,:302,:309,:322), and
`lenny_slot_pod_replacement_total` is incremented only from `accountSlotFailure`'s drain tail,
whose three callers are each gated on `maxConcurrentSessions > 1` (the resume caller through the
non-empty-slot-id guard, which `reserveResumeSlot` makes exactly that at binder.go:1675-1677). Both
spec/16 rows scope themselves to `maxConcurrentSessions > 1`, so neither is falsified.

WATCHOUT: `ReleaseSlotReservation` IGNORES its `slotID` parameter today — the body builds a
`SlotClaimer` and calls `ReleaseSlot(ctx, sandboxName, false, false)` with no reference to
`slotID`. CODE-4 adds a fourth parameter to this function. A reviewer checking that the staged
four-argument call sites "thread the slot id" will find the shipped three-argument form does not
use the third one either; that is not evidence of a defect. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:493-505.

MISTAKE (nearly filed, and here is what killed each of the four):
 (1) "The compensation's added latency breaches a stated SLO / trips a burn-rate alert" — killed by
     the FACT above: both `recordStartupDuration` sites are success-only, and
     `lenny_session_creation_duration_seconds`' phases end at the `session_id` response, which the
     compensation never reaches (a concurrent pool's create-time claim is `ClaimSlot`, connect-only,
     with no `materializeSlot` and therefore no compensation).
 (2) "docs/reference/state-machines.md:251's `slot_cleanup -> leaked` gloss ('when the cleanup
     timeout is exceeded') becomes wrong, because SPEC-3 adds an unacknowledged/unclean reclaim as a
     second producer" — killed by Standing Trap #301 (both spec glosses are already loose; §5.2's
     own 'If cleanup fails, the slot is leaked' is broader; shipped code already sets
     `leaked = err != nil || !cleanly` at every session end) plus the compaction note that the
     disputed `state-machines.md:251` Deferred was retired as closed. The docs mirror is the same
     class as the spec gloss.
 (3) "`lenny_gateway_pod_retirement_total`'s two reasons (`session_count_limit`,
     `scrub_failure_limit`) do not cover the unhealthy-threshold drain CODE-5 newly reaches from two
     more callers" — killed because that drain has its own counter,
     `lenny_slot_pod_replacement_total`, incremented in the same tail, and `applySlotRetryPolicy`
     already reaches it today. Pre-existing at worst, and not a gap.
 (4) "The tier-4 datastore-crossing case's `lenny_adapter_leaked_slots` gauge assertion is
     unobservable in `recycle_scrub_path_test.go`, which wires no session server" — this is a close
     variant of the already-refuted "tier-4 case asserts a re-bind on a second pod its fixture does
     not have", whose refutation answered the whole class as ordinary fixture extension. Do not
     re-dress it as a metrics-observability finding; the verifier pair will land on the same
     refutation.

FACT: `Binder.ReleaseSlot` ignores the recycle `ShutdownRecycle` response (`_, _ =`), so CODE-1's
re-keyed `exited_cleanly` cannot change the leak disposition on the occupancy-zero recycle edge.
For that second RPC the adapter holds no entry, so `removed` is false and the answer is
`true && (false || true)` — byte-identical to the shipped `bound` gate's answer. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:536-545,:560-580.

OPEN (unchanged, restated so it is not lost): Standing #434, "Does the resume-path slot failure owe
an `error_type` value?" is this lens's own item from an earlier round and one dress of it has since
been refuted at the materiality bar. It is a legal-either-way question for the human, not a finding.
Do not re-file it.


### [non-spec.1.review-performance.1]

DECISION: returned an EMPTY findings list — BECAUSE every candidate I built under the
performance / scalability / failure-mode lens either collapsed on the evidence or is a close
variant of something the standing context already records as refuted, settled or a recorded
Open — ALTERNATIVES: I built and dropped six candidates, each listed below with the fact that
killed it, so the next performance lens does not rebuild them.

FACT: the newest delta in the whole staging is the `budget/2` split in CODE-4, and it is sound.
`cl.Shutdown(rctx, sessionID, reason, budget/2)` with the RPC deadline at `budget` reproduces the
shipped §11.4 relation exactly (`userTerminateRPCTimeout = 20s` bounding the call,
`userTerminateDeadline = 10s` as the graceful window). The adapter's worst case for a STARTED
reclaim is `Runtime.Close` bounded by `resolveShutdownGrace` ≈ budget/2 plus a fast
`removeSlotTree`, so it finishes inside the gateway's `budget` with a budget/2 margin;
`drainViaLifecycle` is a fire-and-forget frame send and adds nothing.
EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55,:128-129; pkg/adapter/session.go:259-271;
pkg/adapter/socketruntime.go:455-467; pkg/adapter/mcpruntime.go:294-305,:312-324.

FACT: before the split, `deadlineMs: 0` made `contextWithGraceDeadline` return the RPC ctx
unchanged, so `resolveShutdownGrace` handed `Runtime.Close` the FULL remaining budget and the
close and the gateway's give-up bound expired at the same instant. That is the defect the split
fixes, and it also fixes this caller's `terminate` frame, whose schema requires
`deadlineMs >= 100` while every shipped caller passes 0.
EVIDENCE: pkg/adapter/session.go:324-333 (contextWithGraceDeadline); pkg/adapter/mcpruntime.go:312-324
(resolveShutdownGrace prefers the ctx deadline's remaining); schemas/runtime-ops-events.schema.json:180-183.

FACT: `lenny_session_startup_duration_seconds` is `PodClaim + CredentialAssignment +
AgentSessionStart` taken from the SUCCESSFUL attempt's `BindTimings`, observed once at the launch
boundary. A retried start therefore does not carry the failed attempt's compensation latency into
the §16.5 P95 < 2s / 5s startup SLO. This extends the standing "the §16.5 creation SLO samples
successful creates only" trap to the startup-latency row, which the trap did not name and which is
the row a retried-but-successful start actually samples.
EVIDENCE: pkg/gateway/sessionserver/start.go:3304-3327; spec/16_observability.md:626 (SLO table).

FACT: `waitInQueue` has a poll ticker and a hard `deadline := start.Add(waitBound)`, so a request
whose `ExcludePods` list has grown to cover the whole pool re-enters acquisition at the poll
cadence rather than spinning. There is no hot loop of full pool scans. The FIFO head IS held for
the whole `attempt`, which is the full bind (connect → stage → finalize → setup → credentials →
StartSession), so the compensation adds ≥5s to a head occupancy that already spans deployer setup
commands. That relative argument is why I did not file head-of-line latency.
EVIDENCE: pkg/gateway/sessionserver/queue.go:155-232,:253-278; start.go:2606-2609.

MISTAKE (mine, nearly filed): "the third `accountSlotFailure` caller destroys a warm pod per
failed §7.3 re-attach, where nothing is destroyed today". It dies on the shipped baseline: a
failed `Binder.Resume` leaves the replacement pod's per-pod claim `bound` with no active session,
and §4.6.1 orphan GC drains it once `claimOrphanTimeout` elapses. The pod is lost either way; the
new accounting only accelerates the reclaim, which is a churn-timing change rather than a net
warm-pool consumption change. Anyone re-deriving the Tier-3 correlated-resume-storm framing (review
log Open "Correlated re-attach churn at Tier 3") has to answer this baseline first.
EVIDENCE: pkg/controller/warmpool/gc.go:223-320; podsession/binder.go:1620-1630.

MISTAKE (mine, nearly filed): "a Redis outage turns CODE-5's new reserved-branch caller into a
pool-wide drain storm". Requires a bind failure AND a Redis failure at once, and `ClaimSlot`
cannot place anything during a Redis outage anyway. It is also the same shape as the already-
refuted `applySlotRetryPolicy` dress in the standing Traps.

MISTAKE (mine, nearly filed): "`slothealth.Tracker`'s never-deleted `events`/`leaked` map keys grow
with the resume path's fresh replacement-pod names". `Forget`/`ForgetPod` run only on the unhealthy
tail, so a sub-threshold entry persists — but that is exactly the pre-existing shape the standing
Trap already covers for `applySlotRetryPolicy`, and the per-pod label cardinality is the documented
design of `lenny_slot_pod_replacement_total{pool, k8s_pod_name}`.
EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:56-67,:100-125; sessionserver.go:412-421.

UNVERIFIED: `slotCleanupBudget`'s denominator inverts the intent at the one place it matters most.
On an EXCLUSIVE pool reached through `Binder.Resume`, `max(T/1, 5) == T`, so the path with the
least parallelism gets the LONGEST hold (the spec's own example pool is `cleanupTimeoutSeconds: 60`,
spec/05:412) on a request goroutine that `context.WithoutCancel` has detached from the client. The
shipped `Binder.Release` path has no budget at all and rides the inbound ctx, so this is a change
from fail-fast to hold-for-T on the resume failure path, and on that pool nothing is accounted
(`SlotID` is empty so CODE-5's guard skips) and nothing is released (`releaseResumeSlot` no-ops).
I did NOT file it: Settled ("no stated budget is breached") and Open ("Nothing at any tier pins that
the compensating Shutdown stays inside its budget") both already record it, CODE-4's own text argues
the direction explicitly, and a §16.5 resume SLO does not exist. A human deciding whether the budget
wants a cap independent of `cleanupTimeoutSeconds` is the right closer, not another lens.
EVIDENCE: non-spec-changes.md:415-419,:448-456; podsession/binder.go:1674-1722; spec/16:626 (no
resume row in the SLO table).

FACT: verified the whole CODE-4 call-site table against the tree. `ReleaseSlotReservation` is
three-argument today at exactly the five production sites plus the `slotBinder` interface, and
`rollbackClaim` is a real function whose slot arm is the sixth row.
EVIDENCE: podsession/slotbinder.go:172,:217,:493; binder.go:1714; sessionserver/start.go:2727,:2834,
:3242-3250.

USEFUL [Standing context, Traps "MISTAKE nearly filed, three capacity dresses in the round-2
performance lens" and "MISTAKE nearly filed, five performance dresses"]: these two entries between
them pre-refuted four of my six candidates and saved a full verification round. Keep them.


### [non-spec.1.review-reliability.1]

DECISION: returned zero findings — BECAUSE every recovery/retry mechanism I traced under crash, restart, cancelled-context and store-failover either holds, is already recorded as an accepted failure mode in the proposal, or sits in the barred trap/refuted list — ALTERNATIVES: I built and dropped five candidates, each listed below with the ground that killed it, so a later round does not rebuild them.

FACT: the round's newest text (the only delta since `non-spec-recheck-2-r2`) is the `budget/2` graceful-window split, and every citation in it resolves. `pkg/adapter/session.go:260` is `s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())`; `schemas/runtime-ops-events.schema.json:180` is `"deadlineMs": {"type":"integer","minimum":100}` and `:183` is the `required` line; `spec/28_communication-channels.md:1082` is the `terminate` row; `pkg/adapter/session.go:309-321` is `drainReason`; `cmd/lenny-gateway/user_revocation.go:50,:55` are `userTerminateDeadline = 10s` and `userTerminateRPCTimeout = 20s`, a real 2:1 precedent for the budget:budget/2 relation. EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55,:128-129.

FACT: the `budget/2` fix actually works, and here is the arithmetic nobody wrote down. The adapter enforces NO per-slot cleanup timeout of its own: `grep -rn "cleanupTimeout\|CleanupTimeout" pkg/adapter/*.go` returns only `podscrub.go:128`, the WHOLE-POD scrub, and the gateway sends `cleanupTimeoutSeconds` on the per-slot `Shutdown` only inside the recycle disposition. So the adapter's per-slot `Shutdown` work is bounded by the inbound RPC ctx (`budget`) and by `contextWithGraceDeadline(ctx, deadline_ms)` (`budget/2`) alone, and the worst case is `budget/2` + drain + `removeSlotTree` + an optional `reportSessionScrub`, comfortably inside `budget`. A reviewer who assumes §5.2's `max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)` is enforced adapter-side concludes the adapter can consume the whole budget and the false-leak defect survives the fix. It cannot. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545 ("minimum 5s enforced at runtime by the adapter"); pkg/adapter/podscrub.go:128; pkg/adapter/session.go:262-266.

FACT: `Client.Shutdown`'s fourth parameter is `deadline time.Duration`, not milliseconds, so the staged `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", budget/2)` type-checks and `int32(deadline.Milliseconds())` is applied inside `shutdown`. A reviewer expecting an int32 ms argument will read the staged call as a type error. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-822.

FACT: `slotbinder.go:542` is the ONLY production reader of the `Shutdown` bool. `grep -rn "\.Shutdown(" pkg/ cmd/ --include=*.go | grep -v _test` gives three adapter-RPC call sites: `binder.go:2043` and `user_revocation.go:129` both discard the bool, and only `slotbinder.go:542` computes `leaked` from it. That is the whole blast radius of CODE-1's re-keying of `exited_cleanly`.

FACT: `resumeOnPod`'s two branches are mutually exclusive — the snapshotless rebuild returns at start.go:3977 before the checkpoint-restore branch is reached — so CODE-5's third `accountSlotFailure` caller on the `podBinder.Resume` failure branch (start.go:4041-4043) cannot double-account with `applySlotRetryPolicy`'s caller on the rebuild path. EVIDENCE: pkg/gateway/sessionserver/start.go:3943-3978,:4041-4043.

MISTAKE (nearly filed, five reliability dresses in one round; each is dead and here is what kills it):
1. "`ReleaseSlotReservation`/`releaseResumeSlot` run on the inbound ctx, which is dead in exactly the expired-context case the compensation exists for, so `relErr != nil` is deterministic and a completed reclaim is recorded as a permanent leak that drains a healthy pod." True as mechanics, and dead: for `applySlotRetryPolicy` it is entirely shipped behaviour (Settled "adapterclient applies no per-RPC timeout"), and for the two NEW accounting paths the proposal states the consequence outright at non-spec-changes.md:614-621 and :1120-1123 and accepts it. Close variant of the already-refuted "Redis outage turns the `relErr != nil` arm into a drain storm".
2. "A `Shutdown` the adapter executed whose response was lost is recorded `leaked`, holding Redis occupancy for a slot that was released, with no reconciliation." Identical shape ships today on every ordinary session end (`Binder.ReleaseSlot`'s `leaked = err != nil || !cleanly`), and the durable-backing hole is already a summary out-of-scope row.
3. "CODE-4 adds a new producer of the pod-global §15.4.2 `terminate` frame on a pod whose only co-tenants are registered-but-unbound, telling the shared runtime to exit under a bind about to `StartSession`." The ordinary session end reaches the identical state through the identical call, and `TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2` pins that firing as intended. Same class as the barred §11.4-revoke trap.
4. "The `materializeSlot` wrapper claims no stage can be added without the compensation, but the compensation is gated on `errors.As(err, &sbe)`, so a future stage returning a plain error is silently uncompensated." Every current branch returns `b.slotBindError(...)` (slotbinder.go:284-325), and the gate is forced because `sbe.Leaked` is where the outcome goes. Below the bar on staged text.
5. "A create-time-reserved bind failure at `maxConcurrentSessions: 2` now drains the pod through CODE-5's reserved caller while the row keeps `PodAssignment`, so the client's retry dials a drained pod." Already the standing Open "Does a §15.1 retry reach a pod already stamped for drain?", already covered by the barred "reserved-branch drain composite is strictly better than today" trap, and stated in the proposal at non-spec-changes.md:634-638.

WATCHOUT: `pkg/adapter/sessionscrubreporter.go:79-81` says "the next release's idempotent re-report are the backstops" for a failed `ReportSessionScrub`. That is false against `RecordSessionScrub`, which has no per-session dedup key. It is a pre-existing code comment in a file no deliverable opens, so it is not 0081's, but a later reliability lens will find it and should not spend a round on it. EVIDENCE: pkg/adapter/sessionscrubreporter.go:78-84; pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481.

UNVERIFIED: whether the staged `deadlineMs = budget/2` window is ever SHORTER than what a Full-level runtime needs to quiesce on the third residue class (a reclaim of a session the runtime was given). The floor puts it at 2500ms and the reclaim is tearing down an abandoned session, so I judged it inert, but nobody has checked it against a real runtime's quiesce time. A conformance or tier-10 lens is better placed.


### [non-spec.1.review-security.1]

DECISION: returned an empty findings list — BECAUSE every security-flavoured candidate I built
either resolves to a pre-existing property this proposal improves or leaves untouched, is already
recorded in the proposal's own out-of-scope / accepted-failure sections, or is barred by a standing
Trap. ALTERNATIVES rejected are enumerated below with the evidence that killed each, so a later
security pass does not rebuild them.

FACT: the credential-material half of the change is a strict improvement, verified end to end.
`assignCredentialsSlot` writes `/run/lenny/slots/{sessionId}/credentials.json` and arms the §4.9
timers at `AssignCredentials`, i.e. BEFORE any start (pkg/adapter/slotcreds.go:23-52). The shipped
`Shutdown` gates the tree removal on `bound := removed && st.sessionID != ""`
(pkg/adapter/session.go:239, the removal at :270), while `deregisterSlotLocked` runs
unconditionally at :238 and cancels every armed timer. So the shipped tree ALREADY produces
"credential file on disk with its enforcement timer disarmed" for a registered-but-unbound entry;
CODE-1's move of `removeSlotTree` under `removed` closes that, it does not open it. Any future
round tempted by "the timer cancellation strands the credential file" (standing Open, review-log:423)
should price it against this: the ordering hazard is shipped and CODE-1 narrows its domain.

FACT: the process-group kill SPEC-3's widened §5.2 action list carries is implemented NOWHERE in
the adapter. `grep -rn 'Setpgid|Pgid|process group' pkg/ cmd/ --include=*.go` returns only
pkg/embedded/k3s/* and `pkg/adapter/workspace/setup.go:202-205` (setup commands get their own
process group for the per-command cap kill). The `Shutdown` handler kills no process group at all.
SPEC-3 extends the bullet's cleanup to the pre-`running` reclaim, which formally extends an
obligation the code has never discharged. NOT FILED: the sentence "On slot completion or failure,
the adapter ... kills any processes owned by the slot's process group" is shipped spec text
(spec/05_runtime-registry-and-pool-model.md:545) and already unimplemented for every ordinary
session release; adding one more covered case is a pre-existing conformance gap widened, not
created. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; pkg/adapter/session.go:227-292.

FACT: `Leaked`'s authority is the adapter's `exited_cleanly` self-report
(`cleanly, err := cl.Shutdown(...)`, non-spec-changes.md:419-425), and after CODE-4/CODE-5 that
self-report gates three things: the Redis counter decrement, the persistent §5.2 unhealthy count,
and the `ExcludePods` placement filter. Under lens check (2) this looks like a bound sourced from
an in-pod component. It is NOT a finding: the identical dependence is shipped at
pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543 (`leaked = err != nil || !cleanly` on
EVERY session end), the proposal names it in its own shipped-defects section (summary.md:424-454),
and each of the three new consumers is strictly more conservative than today (today there is no
exclusion at all, and the reserved/resume paths reach no tracker at all). A misreporting adapter
gains nothing it does not already have.

FACT: tenant pinning survives the `leaked` hold and the new filter, checked in the code rather than
assumed. `ReleaseSlot(leaked=true)` returns before the DECR and before `DeleteClaim`
(pkg/gateway/podlifecycle/podclaim/slotclaimer.go:833-838), so the residue-bearing pod keeps a live
per-pod claim. `ClaimSlot` pass 1 skips any claim whose `Spec.TenantID` differs (:428-432) and pass
2 skips any pod that HAS a claim (:485-491). A pod holding an unacknowledged reclaim therefore
cannot be handed to a second tenant by either pass. The `ExcludePods` skip is a `continue` beside
`expiredByUptime`, which sits AFTER the tenant check in pass 1, so `sawTenantMismatch` semantics do
not move; and both `ErrTenantMismatch` and `ErrNoConcurrentSlot` map to `WARM_POOL_EXHAUSTED` with
the same reason at slotbinder.go:436-441, so the exclusion mints no new client-visible outcome
either way.

FACT: the resume path mints no credential lease, confirmed from both ends, so CODE-4's "releases no
credential lease" on that branch is right and not a lease-stranding hole. `Binder.Resume`
(pkg/gateway/podlifecycle/podsession/binder.go:1591-1663) calls `connect` → `reserveResumeSlot` →
`cl.Resume` and nothing else, and `resumeOnPod` (pkg/gateway/sessionserver/start.go:4005-4068)
issues no credential call before or after it. The only slot-path minter in the tree is
`assignSlotCredentials`, called once, inside `materializeSlot`
(pkg/gateway/podlifecycle/podsession/slotbinder.go:307). That is also why CODE-4's UNCONDITIONAL
`b.releaseCredentials(req.SessionID)` in the `materializeSlot` wrapper is safe: no lease for a slot
session is minted outside the stages that wrapper owns, so it can never revoke a lease some other
live component still needs.

FACT: the hold-state allowlist the accepted-failure bullet cites is exactly as described — five
methods, `CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, `Health/Check`, `Health/Watch`
(pkg/adapter/holdstate.go:52-58), so a compensating `Shutdown` into hold state is refused and
correctly classified `leaked`. Citation verified, not a finding.

WATCHOUT: the strongest security candidate in this proposal is NOT on the failed-bind path, and it
is the one a later lens will most likely rebuild. CODE-1 regates `reportSessionScrub` from `bound`
to `live` (runtimeLive membership), and CODE-2 makes `noteRuntimeStarted` refusable. Together they
open a window on a SHIPPED trigger rather than a new one: a §11.4 full revoke, or any gateway-sent
`Shutdown`, that lands while a start is in flight (`st.started` true, `runtimeLive` empty) today
files a `ReportSessionScrub` and advances `sessions_served`; after CODE-1 it files none, so
`recycle.maxSessionsPerPod` does not advance for a session whose `Runtime.Start` may already have
registered in the socket runtime's active set (pkg/adapter/socketruntime.go:184,:220). I did NOT
file it. Three reasons, in order of force: (1) it is a close variant of the twice-barred
"withheld report as a residual-state relaxation" family (review-log Traps, `MISTAKE nearly filed
twice`); (2) the whole-pod scrub at occupancy zero remains the backstop and `maxSessionsPerPod` is
hygiene rather than an isolation boundary, so the lens's "merely less strict than it could be is
NOT a finding" bar applies; (3) the window is the proposal's deliberate contract — it withholds the
count for a session the shared runtime process was not given, which is exactly what SPEC-3's
because-clause states. A later round wanting this needs NEW evidence that a revoke-versus-start
interleaving is reachable in a running deployment AND that the un-advanced count is load-bearing
for an isolation property, not just for pod churn. EVIDENCE: pkg/adapter/session.go:243,:270-281;
non-spec-changes.md:99-138.

WATCHOUT: `sdkwarm.go:261` becomes `_ = s.noteRuntimeStarted(...)` with NO rollback
(non-spec-changes.md:328-333), so a refusal there leaves `ConfigureWorkspace` succeeding while
`runtimeLive` does not hold the session — `runtimeIdleLocked` true and `soleSessionLocked` empty
for a live session. I traced it and did not file: the refusal needs the entry to vanish between
`claimSessionSlotUnderLock` (sdkwarm.go:217) and the record, which on an SDK-warm pod
(`maxConcurrentSessions: 1` by §6.1) needs a concurrent `Shutdown` or `DemoteSDK`, and the
downstream effects are all fail-CLOSED (a cancelled MCP surface, an empty control-event stamp)
rather than fail-open. The proposal's stated ground for the no-rollback ("no compensation races it")
is true as written; the residual risk is a DIFFERENT racer, and it is narrow. Anyone reopening this
must show a production path that removes the entry mid-`ConfigureWorkspace`.

FACT: no §10.3 / §13.2 / §13.1 surface is touched. The change adds no agent-pod apiserver path, no
egress rule, no ServiceAccount, no admission webhook, no credential-file mode or group change, and
no new wire field (`schemas/lenny-adapter.proto` is never opened, per rule S-2). The compensating
`Shutdown` rides the gateway→adapter connection the failing stage already holds. I checked
`spec/13_security-model.md:26,:28,:30` and `spec/17_deployment-topology.md:54` against the staged
edits and none of them goes stale.

MISTAKE [none of mine, but recorded for the next security pass]: I burned time re-deriving
"§7.2 releases a replacement pod carrying a started tenant runtime back to the pool" as a
cross-tenant exposure. It dies on `ReleaseSlot(leaked=true)`'s early return: the per-pod claim
survives, so `ClaimSlot` pass 2's `if found { continue }` (slotclaimer.go:485-491) excludes the pod
from fresh acquisition and pass 1's tenant check pins it. On the exclusive resume branch
`releaseResumeSlot` is a no-op and nothing releases the whole-pod claim at all, so the pod is
reclaimed by §4.6.1 orphan GC (a drain) rather than returned to inventory. This is the same
evidence base as the eight-times-refuted exclusive-pod family; do not rebuild it.

UNVERIFIED: whether `spec/05_runtime-registry-and-pool-model.md:545`'s process-group-kill clause has
ANY implementation anywhere (I grepped `pkg/`, `cmd/`, `sdks/` for Setpgid/Pgid/"process group" and
found none in the slot-cleanup path). If a conformance lens confirms that, it is a §5.2 spec-vs-code
gap worth its own finding against the tree — not against 0081, which only restates the sentence.


### [non-spec.1.review-test-coverage.1]

DECISION: returned an EMPTY findings list. — BECAUSE the staging this lens owns has not
moved since the last time this lens ran and returned empty. `diff -rq` over the whole
`scratchpad/cp-snap/0081` tree shows `...non-spec-changes.md` byte-identical to the
`non-spec-recheck-2-r3`, `-r4` and `-r5` snapshots, and the only files that differ from
`non-spec-recheck-2-r5` are `summary.md` (one impacts-table cell for 0078) and
`problem-statement.md` (one paragraph re-stating 0078's sequencing). Neither touches a
test listing, a tier, a fixture, or a behaviour. — ALTERNATIVES rejected, each with its
reason: (1) "the exclusive-pool `Binder.Resume` compensation has no listed assertion that
the `Shutdown` is sent" — the resume bullet does carry an exclusive-pool sub-case ("On an
exclusive pool the resume reserved no slot, so neither arm runs"), so this is a preference
about how much that sub-case asserts, and the standing Open "Nothing at any tier pins that
the compensating `Shutdown` stays inside its budget" already records the same surface as
deliberately not filed. (2) "the workspace-prep stage is missing from the per-stage
compensation table" — `FinalizeWorkspace` also runs `ensureSlotPaths`, so the finalize row
already exercises "compensate a registered-but-unbound entry" at the gateway level, where
the adapter is a fake and the stage identity is the only difference. (3) "no test asserts
the new accounting callers actually reach `DrainSandbox`" — the helper tail is unchanged
and the tier-1 cases assert each new caller reaches the helper. (4) tier 3 / 5 / 8 / 9 —
all four already declined, three of them on the refuted list.

FACT: every shipped-test citation in `## Testing` resolves. `req(pool, maxConcurrentSessions)`
is `pkg/gateway/sessionserver/slotretry_test.go:69`; `TestSlotBindErrorReason_spec_5_2` is
`:383` and really does carry `{"session_start", codes.PermissionDenied,
SlotReasonPolicyRejection}` at `:390`, so the staged `codes.Aborted` row lands beside a real
row; `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` is `:468`;
`TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` is `pkg/adapter/socketruntime_test.go:252`;
the "bounded read" is `fr.readWithin(750*time.Millisecond)` at `pkg/adapter/slotsession_test.go:210`.
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` matches the staged
plan: `generalSlotEdges` :32-37, positive loop :55, negative loop :70, `requireAllContain`
over the doc section :103-110. Do not re-derive this set.

FACT: `cl.Shutdown`'s fourth parameter is `deadline time.Duration`, converted with
`int32(deadline.Milliseconds())` (pkg/gateway/runtime/adapterclient/client.go:807,:817), so
CODE-4's `budget/2` is type-correct and the 2500ms-floor arithmetic behind the
`schemas/runtime-ops-events.schema.json` `minimum: 100` claim holds.

FACT: the tier-1 gateway file list (`slotbinder_test.go`, `slotretry_test.go`,
`start_test.go`) mixes packages: `slotretry_test.go` is `package sessionserver` (internal)
while `start_test.go` is `package sessionserver_test` (external). The staged resume-path
case asserts `MarkLeaked` on `s.slotStates`, which `sessionserver.go:1873` builds with
`slotstate.NewRegistry()` and exposes through no `Options` field, so that case can only
live in the internal file. `Options.SlotHealth`, `SlotReplacement` and `SlotLeakGauge` ARE
injectable (sessionserver.go:1658-1670,:1872-1875), so the gauge and ledger halves are
reachable from either package. Not a finding — the internal file is listed — but a fixer
who puts the resume case in `start_test.go` will not compile.

WATCHOUT: the newest text in the whole staging is CODE-4's `deadlineMs: 0` → `budget/2`
grace split and the tier-1 assertion that pins it. That assertion needs the gateway fake to
capture `ctx.Deadline()`, and the fixture paragraph's enumeration of what `concurrentAdapter`
gains does not name it. EVIDENCE: non-spec-changes.md:824-836 (the enumeration) versus :841-844
(the assertion); pkg/gateway/podlifecycle/podsession/binder_test.go:1167
(`recordingShutdownAdapter.Shutdown(_ context.Context, req …)` discards the context). I did
NOT file it, and neither did the previous run of this lens: the material skeptic has already
refuted the structurally identical tier-7a "no seam is staged" finding as test mechanics
resolvable at implementation time. A fixer already editing that paragraph should add the
clause; a reviewer should not spend a finding on it.

USEFUL [non-spec-recheck-2.4.review-test-coverage.1]: its four rejected alternatives and its
`Shutdown`-caller enumeration are still accurate against the tree and saved me from
re-deriving the whole shipped-test disposition. Its WATCHOUT on the fake's missing deadline
capture is the one live residual and is repeated above so it does not get lost in compaction.

USEFUL [Traps, "MISTAKE nearly filed, four test-coverage dresses"]: item (1) (the `Resume`
rollback's deterministic tier-1 framing) and item (3) (the `isTransientPodClaimError`
preservation claim) were both on my own candidate list before I read it. I re-verified (3)
independently: `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3682)
matches only three types and five sentinels and `SlotBindError.Unwrap` returns the cause
(pkg/gateway/podlifecycle/podsession/slotfailure.go:75), so the new wrapper moves no
classification and owes no test.


### [non-spec.2.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE every class this lens owns (forward
reference, underspecified target, content-losing relocation, ordering/gate state, unresolvable
anchor, code-against-unlanded-spec, and the checklist) was walked end to end against the tree and
came back clean; the only residues I found are already recorded in Standing context as OPEN,
DEFERRED, or refuted — ALTERNATIVES: I built and dropped four candidates, each named below with
what killed it.

FACT: the snapshot the brief names (`scratchpad/cp-snap/0081/non-spec-r2`) is byte-identical to the
live proposal, and so is `non-spec-r2-start`. The newest snapshot that differs at all is
`non-spec-r1-start`, and its ONLY differing file is the review log. The last real staging delta is
`non-spec-recheck-2-r5` → `non-spec-r1-start`, and it is three files: one clause appended to the
0078 impacts row ("it edits no file 0078's TEST-7 rewrites, so that last overlap is package
co-location rather than a file collision"), plus problem-statement and review-log churn. There is no
fix-stage delta to read hardest this round. EVIDENCE: `diff -rq` over all 57 snapshot dirs.

FACT: the 28-fenced-block anchor sweep re-run clean on 2026-09-09. A python count of each fenced
block of `.spec-changes.md` against `spec/*.md` gives exactly 1 for blocks
0,2,5,8,10,12,14,16,18,20,22,24 (the anchors) and 0 for every replacement/insertion block, including
the §29.4 appended sentence and both SPEC-4 blocks. Do not re-run it unless `spec/` moves.

FACT: SPEC-3's `**Scrub model.**` append is safe against all four §5.2 tier-11 first-match gates,
verified by reading the gate source rather than by trusting the standing note. `lineContaining` is
`strings.Contains` per line, case-sensitive (tier11_docs/backup_status_enum_test.go:48-55), and the
gates anchor on capitalised "Whole-pod replacement trigger" (concurrent_slot_lifecycle_doc_
reconciliation_test.go:92), "Session count limit" (:155) and "Uptime limit" (:298), while the append
writes "whole-pod replacement trigger stated below" in lower case. The append DOES contain the
literal "**Slot cleanup:**", and no tier-11 gate anchors on that phrase — grep over `tests/` returns
only comment text. `requireLine(s52, "The gateway triggers the whole-pod scrub")`
(recycle_scrub_trigger_consistency_test.go:69) is likewise unreachable from the append.

FACT: SPEC-1's §4.7 row replacement keeps every string the two shipped §4.7 gates read.
`TestRecycleTriggerConsistentAcrossSpec47And52_F5215` scopes to `requireLine(s47, "| \`Shutdown\` |")`
and requires "recycle disposition", "ReportPodScrub", "does not block the response on the scrub",
and the three backticked params `podId`/`cleanupCommands`/`cleanupTimeoutSeconds` — all of them sit
in the row remainder SPEC-1 explicitly leaves untouched. `TestRecycleTriggerCrossRefsResolve_F5215`
additionally requires the row to carry the literal
`05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes`; the replacement
adds one of its own, so the link survives even if the remainder's were ever reworded. EVIDENCE:
tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:54-100,:129-146.

FACT: SPEC-3's new path literals pass both retirement sweeps. It writes
`/run/lenny/slots/{sessionId}/` and `credentials.json`, which is the CURRENT per-slot path the
credential sweep demands, and the session placeholder spelling the slot sweep demands
(`{slotId}`/`<slotId>` are the retired ones). EVIDENCE:
tests/tier11_docs/credential_path_literal_sweep_test.go:1-20;
tests/tier11_docs/slot_placeholder_literal_sweep_test.go:34-41.

FACT (closes the standing Open "Does `spec/18` need an edit?"): no. `grep -n "slot\|scrub\|recycle"
spec/18_*.md` returns spec/18:235 (workspace-plan validators, per-slot layout pointer) and
:531-533 (the phase-12c deliverable lines naming "the per-session slot cleanup and whole-pod scrub
split", concurrent slots, service mode) plus the :538 exit criteria. None enumerates a precondition,
a teardown gate, a report rule, or a per-slot edge, so nothing in spec/18 goes stale under SPEC-1
through SPEC-4 and no phase deliverable of this proposal depends on a later phase's artifact.

FACT: `pkg/gateway/sessionserver/start_test.go` is `package sessionserver_test` (EXTERNAL), while
`slotretry_test.go`, `queue_internal_test.go`, `start_preclaim_internal_test.go` and
`terminal_reclaim_internal_test.go` are `package sessionserver` (internal). `start_pod_test.go` is
also external and only MENTIONS `resumeOnPod` in a comment at :1420 — it drives the handler, not the
function. So of the three files the gateway tier-1 section names, only `slotretry_test.go` can call
`applySlotRetryPolicy`, `bindConcurrentSlot` or `resumeOnPod` directly; a case placed in
`start_test.go` must observe through the exported wiring (`SlotLeakGauge`, `SlotReplacement`) or
through the HTTP handler. This is NOT filed: the proposal names three files for eleven cases without
assigning cases to files, so file choice is implementation judgment and the external route is
workable. A fixer who ever pins a case to `start_test.go` must check this first.
EVIDENCE: pkg/gateway/sessionserver/start_test.go:3; slotretry_test.go:3; queue_internal_test.go:3.

FACT: `pkg/adapter/socketruntime_test.go` is `package adapter_test` (external) while
`slotsession_test.go`, `export_test.go`, `usage_test.go`, `adapterevents_test.go` and
`podmcp_arming_internal_test.go` are `package adapter`. The "Co-tenancy hazard" case is split
correctly across that boundary: its unexported-field assertions belong in `slotsession_test.go`, and
the sibling assertion it puts in `socketruntime_test.go` beside
`TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (:252-300) is achievable from outside, because
that shipped test already observes the connection behaviourally (EOF on the reader) rather than by
reading `p.conn`. Not a finding; recorded so nobody re-derives the split as a defect.

MISTAKE (mine, nearly filed and withdrawn): "the checklist's tier-4 work is split across S9 and
S10, because the datastore-crossing case's leaked-release half is CODE-4's and its re-bind half is
CODE-5's." Both steps list tier 4, so neither omits a tier its deliverable reaches, and the lens's
checklist clause is about omitted tiers rather than about which step authors which half of a case.
Same for S8 and S9 both listing 7a.

MISTAKE (mine, nearly filed and withdrawn): "S5 carries lane `docs` yet edits a Go test file under
`tests/tier11_docs/`." The one-lane-per-step rule the lens states is spec-versus-non-spec, and the
tier-11 edit is inside DOCS-1's own deliverable text ("DOCS-1 therefore carries the tier-11 work",
non-spec-changes.md:730-732). One deliverable, one step, one lane.

MISTAKE (mine, nearly filed and withdrawn): the `fakeSlotBinder` fixture claim. Three staged tier-1
cases assert on the `ExcludePods` a `BindSlot` call carries, and one of them says it runs "over the
shipped `fakeSlotBinder`" (non-spec-changes.md:880), whose `BindSlot` discards its request outright
(`func (f *fakeSlotBinder) BindSlot(_ context.Context, _ podsession.SlotBindRequest)`,
slotretry_test.go:38). The fake needs a recorded-requests field, exactly as `released [][2]string`
(:31) needs a third element for the `leaked` disposition. Withdrawn because the `released` variant of
this finding was already built and REFUTED as "ordinary test authoring forced by the case the
proposal already stages", the file is in the edit list, and Standing Traps already carries the note.

USEFUL [Standing context, Settled 70/228]: the sixteen-anchor list and the "run a script over the
fenced blocks" instruction. It let me re-verify every anchor in one command instead of sixteen reads.
USEFUL [Standing context, Settled 369]: the first-match casing hazard on the §5.2 append. It told me
exactly which four gate anchors to check and why, and the check confirmed the append is safe.
USEFUL [Standing context, Settled 178/179]: the "checklist is clean, do not re-derive" entry. I
re-derived it anyway (ten steps, ten deliverables, one lane each, spec block leading, every
Depends-on earlier, no checked box) and it holds; the entry was right and cost me only minutes.


### [non-spec.2.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in the four
proposal files resolves and says what the proposal claims; the only drift found is
sub-line-range (e.g. `generalSlotEdges` cited `:32-36` where the slice literal spans `:32-37`;
`oplock.go:82` where the `codes.Aborted` word is on `:83`), which the brief excludes.
ALTERNATIVES: filed nothing on the two loose-prose candidates below, because every
structurally identical candidate this loop has produced was refuted on the materiality bar.

FACT: the whole-staging citation sweep really is clean, and it stays clean after the
budget-split fix. I re-opened, byte for byte: the 28 fenced anchor blocks (12 anchors at
count 1, all replacements at count 0, script re-run against the live `spec/`), every
`file:line` in the non-spec, spec, summary, problem-statement and checklist files (a range
check over all five found zero out-of-file citations), and by hand every load-bearing one in
`pkg/adapter`, `pkg/gateway/podlifecycle/{podsession,podclaim}`, `pkg/gateway/sessionserver`,
`pkg/sandbox/slotstate`, `cmd/lenny-gateway`, `schemas/`, `spec/04,05,06,07,28,29`, and the
tier-4/7a/11 test files. Do not re-run it; re-run only the anchor script if `spec/` moves.
EVIDENCE: the anchor script's shape is recorded at review-log.md:228.

FACT: the newest text (the `budget/2` graceful-window split, landed after the last citations
lens ran) is citationally sound end to end. `userTerminateRPCTimeout` (20s) does exceed
`userTerminateDeadline` (10s) and is passed exactly as the doc comment describes
(cmd/lenny-gateway/user_revocation.go:50,:55,:128-129); `userRevokeReason = "USER_REVOKED"` is
at :45; the adapter passes `ShutdownRequest.deadline_ms` straight into the `terminate` frame
(pkg/adapter/session.go:260) and `drainReason` normalises at :309-321; the frame's schema
requires `deadlineMs` with `minimum: 100` (schemas/runtime-ops-events.schema.json:180,:183)
and the 5s budget floor puts `budget/2` at 2500ms; `spec/28_communication-channels.md:1082` is
the `terminate` row. EVIDENCE: cmd/lenny-gateway/user_revocation.go:47-55.

WATCHOUT: non-spec-changes.md:442 says "No code under `pkg/`, `cmd/`, or `sdks/` reads the
reason value" two sentences after the same paragraph states that `drainReason` reads it
(:431-432). Literally self-contradictory, recoverable meaning ("nothing branches on it beyond
the normaliser"), and the conclusion is unaffected. I verified it: `req.GetReason()` has
exactly one non-test reader, pkg/adapter/session.go:260, which hands it to `drainReason`; the
SDK's `sd.Reason` reads (sdks/runtime/go/runtime/runtime.go:523, lifecycle.go:296) are the
frame's reason, not `ShutdownRequest.reason`. NOT FILED as wording. A fixer touching that
paragraph should narrow it to "nothing branches on the reason beyond `drainReason`".
EVIDENCE: pkg/adapter/session.go:260,:303,:314-321.

WATCHOUT: `pkg/adapter/sdkwarm.go` has a SECOND writer that clears `s.sdkConnected` besides
`DemoteSDK` — `ShutdownDemoteSDK`'s timeout branch at sdkwarm.go:104 — so CODE-2's "which only
`DemoteSDK` clears" (non-spec-changes.md:333) is loose. The conclusion (the SDK-warm site must
keep the `releaseSessionSlot`+`DemoteSDK` idiom rather than take a bare `Runtime.Close`) is
untouched, because :104 is a process-shutdown path. NOT FILED. EVIDENCE:
pkg/adapter/sdkwarm.go:104,:287.

USEFUL [Settled, "The whole-staging citation sweep is COMPLETE"]: review-log.md:254 saved a
full re-derivation. I spot-checked roughly forty of its citations across nine packages and
found no drift, which is the third independent confirmation; treat it as closed.

USEFUL [Traps, "Grepping spec prose for a quoted phrase fails SILENTLY when the phrase wraps"]:
review-log.md:386. `spec/29:697`'s "adapter closes the session runtime" really does start
mid-sentence after `:696` ends on "the", and the §29.4 Preconditions citation `:586-588` /
`:589-591` splits on the same kind of wrap. A one-line grep returns nothing for either.


### [non-spec.2.review-client-surface.1]

DECISION: returned zero findings — BECAUSE every externally-consumed contract this proposal touches was
opened and confirmed intact or explicitly, correctly deferred — ALTERNATIVES: filing the
`docs/reference/adapter-contract.md:75` staleness again (already refuted by the material skeptic and
recorded as DEFERRED eleven times), filing the proto comment staleness (rule S-2 / R1b, recorded
DEFERRED), and filing the "no code reads the reason value" self-contradiction (see FACT below) —
all three are below the bar.

FACT: the snapshot at `scratchpad/cp-snap/0081/non-spec-r2` is BYTE-IDENTICAL to the live proposal
directory (`diff -rq` returns nothing), so the "read the changed sections hardest" instruction had no
diff to work from this round. Do not spend time on the diff; read the whole document.
EVIDENCE: scratchpad/cp-snap/0081/non-spec-r2 vs proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry

FACT: the whole terminate-frame chain the compensation newly drives is verified end to end and is
CLEAN. `cl.Shutdown(rctx, sess, "slot_bind_failed", budget/2)` → `DeadlineMs: int32(deadline.Milliseconds())`
(pkg/gateway/runtime/adapterclient/client.go:807,:816) → `drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())`
(pkg/adapter/session.go:260) → `Lifecycle.Terminate(deadlineMs, drainReason(reason))` (:303). `drainReason`
(:309-321) defaults every unrecognised value to `session_complete`, so `"slot_bind_failed"` mints no wire
value; `ShutdownRequest.reason` is a bare `string` with no enum (schemas/lenny-adapter.proto:1611). The
budget's 5s floor puts `budget/2` at 2500ms, satisfying the schema's `deadlineMs` `minimum: 100`
(schemas/runtime-ops-events.schema.json:180-183) on every pool configuration. Every one of the proposal's
citations in that paragraph resolves exactly, including spec/28_communication-channels.md:1082 and
cmd/lenny-gateway/user_revocation.go:45,:129.

FACT: CODE-1's re-key of `ExitedCleanly` to `closeErr == nil && (live || treeErr == nil)` breaks NO shipped
wire-contract or conformance test, and I checked each by hand. Every existing driver starts its session
through `StartSession` first, so `live` is true and the expression collapses to today's `closeErr == nil`:
tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:285,:318 (via `startAndShutdownSlot` at :136-152)
and tests/tier10_conformance/recycle_scrub_conformance_test.go:197,:393. The occupancy-zero recycle
`Shutdown` also still answers cleanly: `removed` is false there, so `treeErr` stays nil and
`(live || treeErr == nil)` is true. `ShutdownResponse` carries no proto doc comment to falsify
(schemas/lenny-adapter.proto:1665-1668).
EVIDENCE: pkg/adapter/session.go:238-291; tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:136-152

FACT: the per-slot sub-state edge has exactly two parallel representations and BOTH are staged. Repo-wide
`grep -rn receiving_uploads docs/ schemas/ sdks/ charts/ pkg/ cmd/ spec/` returns only
docs/reference/state-machines.md:234-235 (DOCS-1) and pkg/sandbox/slotstate/slotstate.go:99-100 +
`ValidTransitions` (CODE-3). spec/15_external-api-surface.md:672 states the fine session states are never
returned in external API responses, so there is no OpenAPI, MCP-tool-schema, SDK or CRD mirror to chase.
Nobody needs to re-run this sweep.

FACT: DOCS-1's tier-11 extension is mechanically correct against the shipped gate, and the citations are
exact. `generalSlotEdges` is at :32-36, the positive loop over the either-concurrency block at :55, the
negative loop over the concurrent-occupancy block at :70, and the reference-page `requireAllContain` at
:100-107. `scopedBlock` is sliced `[scopedHeader:generalHeader]` and the scoped header precedes the general
one in spec/06 (:146 vs :150), so adding `"receiving_uploads ──→ slot_cleanup"` to `generalSlotEdges` makes
the positive loop pass and cannot trip the negative loop. The proposed table substring
`` `receiving_uploads` | `slot_cleanup` `` matches DOCS-1's row verbatim.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36,:55,:70,:100-107

FACT: the `Stage: "resume"` CODE-4 mints on the resume path reaches NO client-visible surface and NO metric
label. `lenny_slot_failure_total` is emitted only by `b.recordSlotFailure`, called with the four fixed
stage constants and nowhere from a `SlotBindError` (pkg/gateway/podlifecycle/podsession/slotbinder.go:287,
:294,:302,:309,:322). `Stage` feeds only `SlotBindError.Reason()` (slotfailure.go:96), and the accounting
tail CODE-5 extracts (`MarkLeaked`/leak gauge/`RecordLeak`, `RecordFailure`, `Unhealthy → DrainSandbox →
replacement → Forget → ForgetPod → zero-gauge`, start.go:2833-2872) never reads `reason` — `reason` is
consulted only at :2873, outside the extracted block. So no new error code, category or label value exists.

FACT: wrapping `Binder.Resume`'s error in a `*SlotBindError` changes no §15.1 envelope. `writePodClaimError`
branches on `*SlotFailedError`, not `*SlotBindError` (start.go:193), and `SlotBindError.Unwrap` keeps the
`SetupCommandFailure` / `CredentialAssignmentError` / sentinel matches alive through the chain
(slotfailure.go:74). `isTransientPodClaimError` (start.go:3648-3682) likewise reads only those types. The
only client-visible delta is the free-text `message` string, which is not a contract.

WATCHOUT: the CODE-4 paragraph says "No code under `pkg/`, `cmd/`, or `sdks/` reads the reason value" two
sentences after describing `drainReason` reading it (non-spec-changes.md, the reason-string paragraph). I
verified `drainReason` is the ONLY reader repo-wide (`grep -rn "GetReason()" pkg/adapter` returns
session.go:260 alone), so the paragraph's conclusion holds and only its absolute phrasing is wrong. I did
not file it: the consequence is nil and it is wording. A future lens that spots it should know it has been
checked and declined, not missed.

USEFUL [Standing context → Traps → "Dead end, and here are its five sites"]: the enumeration of the five
`ReportSessionScrub` sites and the note that they all scope to "a session release" saved me a full grep
sweep of spec/, docs/ and schemas/ for the withheld-report claim.

USEFUL [Standing context → Settled → "No per-slot sub-state is client-visible"]: correct and I confirmed it
independently by grep; it is the single most load-bearing fact for this lens and it should survive
compaction.


### [non-spec.2.review-docs-alignment.1]

DECISION: returned an EMPTY findings list — BECAUSE every docs/ surface this change touches
resolves to one of three states: covered by DOCS-1, already barred by a standing trap or a
material-skeptic refutation, or a pre-existing enumeration gap this change only adds one more
instance to — ALTERNATIVES: rejected filing `docs/reference/adapter-contract.md:75` (barred by the
refuted-list entry AND standing Deferred), the `leaked` gloss at `docs/reference/state-machines.md:251`
(barred trap), the `WARM_POOL_EXHAUSTED` cause in `docs/operator-guide/troubleshooting.md` (barred
MISTAKE), and the pod-termination-cause list at `docs/runtime-author-guide/lifecycle.md:430-436`
(same pre-existing-enumeration class: it already omits the shipped `ceil(maxConcurrentSessions/2)`
unhealthy threshold, so one more cause does not clear the bar).

FACT: the docs corpus mirror of the per-slot sub-state machine is EXACTLY ONE table,
`docs/reference/state-machines.md:233-237`, and the general table's four rows sit at :234-237 with
the concurrency-scoped pair described in prose at :251. The `receiving_uploads` hits at
`docs/reference/state-machines.md:149-151,:175-176`, `docs/assets/diagrams/pod-warm-path.svg:26`,
`docs/assets/diagrams/sdk-warm-path.svg:29` and `docs/runtime-author-guide/lifecycle.md:22-52` are
the POD-level warm-path machine (`receiving_uploads → finalizing_workspace → running_setup →
starting_session`), a different vocabulary with no cleanup edge. DOCS-1 is therefore complete for
SPEC-4 and there is no second mirror and no diagram to update.
EVIDENCE: docs/reference/state-machines.md:234-237,:251; docs/runtime-author-guide/lifecycle.md:27,:39

FACT: `exited_cleanly` has ZERO occurrences in `docs/` and `spec/`; the only near-hit is the
RETIRED-name block `docs/api/internal.md:157` (`bool clean_exit = 1;` under `StopSessionResponse`),
which Settled entry 231 already scopes out. CODE-1's re-keying of that field owes no docs edit.
EVIDENCE: docs/api/internal.md:157

FACT: `lenny_adapter_leaked_slots` occurs in `spec/05:545` and `spec/06:160` and NOWHERE in `docs/`
(not in `docs/reference/metrics.md`, whose slot rows are :166-167). SPEC-3 naming the gauge a third
time in §5.2 adds no new docs companion obligation, because the gap is pre-existing on two shipped
sentences. `lenny_slot_pod_replacement_total`'s docs description ("Pod replacements triggered by
slot failures", metrics.md:167) stays true under CODE-5's two new callers, and
`lenny_slot_failure_total`'s (:166) is untouched because CODE-4 mints no new emission site.
EVIDENCE: docs/reference/metrics.md:166-167; spec/05_runtime-registry-and-pool-model.md:545

FACT: no runbook under `docs/runbooks/` carries any slot-level narrative, so the lens's
"new cause absent from the operator docs that ENUMERATE that failure's causes" test has no
enumeration to fail against. `docs/runbooks/warm-pool-exhaustion.md` is pool-inventory-scoped
(symptoms are `RUNTIME_UNAVAILABLE` and `lenny_warmpool_idle_pods`), not slot-placement-scoped, so
the `concurrent_slots_exhausted` route the placement constraint adds does not land in it.
EVIDENCE: docs/runbooks/warm-pool-exhaustion.md:18-22,:44-49

FACT: `docs/tutorials/user-credentials.md:76` ("the pod briefly holds a credential file that is
removed on session end") becomes MORE true after SPEC-3/CODE-1, not less: today a failed bind
strands that file. A docs lens looking for a falsified credential-lifecycle sentence will find the
change strictly improves every one of them. Same for `docs/reference/adapter-contract.md:523` and
`docs/operator-guide/security.md:197`.
EVIDENCE: docs/tutorials/user-credentials.md:76

USEFUL [standing-context Deferred at review-log.md:463]: the entry's CORRECTED half — that
`adapter-contract.md:75` is the only falsified row and `:81`, `security-principles.md:33`,
`execution-modes.md:68`, `multi-tenancy.md:72` are NOT edit sites because "at each session release"
is widened rather than falsified — is what let this lens stop after one grep instead of re-deriving
the five-site split. It is also what makes a fresh finding on any of those four a re-litigation.

WATCHOUT: `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`'s negative loop
(`:70-74`) asserts no `generalSlotEdges` string appears in `scopedBlock`, and `scopedBlock` is
`s62[scopedHeader:generalHeader]`. Adding `"receiving_uploads ──→ slot_cleanup"` is safe today, but
any future edit that moves the scoped block BELOW the general header inverts both slices silently.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:54,:64,:70

OPEN: `docs/reference/configuration.md:99` describes `sessionPolicy.cleanupTimeoutSeconds` as
"Timeout for `cleanupCommands` plus the scrub-report grace" and states the constraint as
"Must be `> 0`". It already omits BOTH the shipped per-slot formula
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` and the shipped CRD rule
`cleanupTimeoutSeconds >= maxConcurrentSessions × 5` (spec/05:545). CODE-4 gives the knob a third
effect (the gateway's reclaim give-up bound, and half of it as the adapter's graceful window).
Not filed: the spec deliberately states no number for the gateway-side deadline, so a docs edit
would document a code-only choice, and the row is already two effects behind. A human or a later
docs sweep should decide whether that row is reconciled to spec/05 as its own change.
EVIDENCE: docs/reference/configuration.md:99; spec/05_runtime-registry-and-pool-model.md:545


### [non-spec.2.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site lens — BECAUSE every identifier the proposal adds, changes or removes was traced to its mirrors and each mirror is either staged, verified-still-true, or already adjudicated in the refuted/deferred ledger — ALTERNATIVES: I built and dropped four candidates (the `lenny_adapter_leaked_slots` gauge named in SPEC-3's append but absent from spec/16 and docs/reference/metrics.md; `docs/reference/adapter-contract.md:75`; the `slot_cleanup ──→ released` gloss at spec/06:156 and docs:236; and the "Spec files touched" §5.2 entry not hinting at the pod-disposition claim). The first is pre-existing (the gauge has only ever been named in spec/05:545 and spec/06:160), the second and third are refuted/deferred, the fourth is the five-times-refuted bookkeeping class.

FACT: **The §4.7 `Shutdown` row that SPEC-1 rewrites IS pinned by a shipped tier-11 gate, and the edit survives it.** `TestRecycleTriggerConsistentAcrossSpec47And52_F5215` and `TestRecycleTriggerCrossRefsResolve_F5215` both do `requireLine(t, s47, "| \`Shutdown\` |")` and then assert the row contains `recycle disposition`, `ReportPodScrub`, `` `podId` ``, `` `cleanupCommands` ``, `` `cleanupTimeoutSeconds` ``, `does not block the response on the scrub`, and the literal link `05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes`. Every one of those lives in the row's RETAINED tail (from "On the default disposition the pod is replaced." onward), which SPEC-1 explicitly leaves unchanged, so the rewrite is green. A future edit that trims that tail turns both tests red. EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65,:70,:74-91,:137-146; spec/04_system-components.md:686.

FACT: **No test, script or lint anywhere greps the string "Slot cleanup".** I checked this because SPEC-3's §5.2 append writes the literal `**Slot cleanup:**` into the `**Scrub model.**` paragraph at spec/05:453, which PRECEDES the bullet of that name, and a `lineContaining`-style gate would have been mis-scoped to the append. `grep -rn "Slot cleanup" tests/ scripts/ pkg/ cmd/` returns nothing. Safe. EVIDENCE: spec-changes.md:465 (the append), spec/05_runtime-registry-and-pool-model.md:545 (the bullet).

FACT: **The four `concurrent_slot_lifecycle_doc_reconciliation_test.go` anchors and the two `slot_definition_glossary_reconciliation_test.go` anchors are all still safe against the CURRENT append text, and the reason is case plus wording rather than luck alone.** Anchors: "Whole-pod replacement trigger", "Session count limit", "Uptime limit", "increments `lenny_gateway_pod_retirement_total`", "**Slot (session mode).**", "A service-mode slot is a different thing". The append writes "whole-pod replacement trigger stated below" in lower case and carries none of the other five. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155,:222,:298; slot_definition_glossary_reconciliation_test.go:84,:127.

FACT: **`ReleaseSlotReservation` has exactly FIVE production call sites plus the interface declaration, and CODE-4's call-site table names all six.** They are slotbinder.go:172 (`ClaimSlot`'s connect-stage release), slotbinder.go:217 (`BindReservedSlot`), binder.go:1714 (`releaseResumeSlot`), start.go:2834 (`applySlotRetryPolicy`), start.go:3246 (`rollbackClaim`), and the interface at start.go:2727. Two test fakes also implement it (`slotretry_test.go:52`, `slotretry_load_test.go:33`) and both files are already in `## Files touched on application (non-spec)`. Nobody has to re-derive this list. EVIDENCE: the grep `grep -rn ReleaseSlotReservation pkg/ tests/ cmd/`.

FACT: **All sixteen "text to replace" anchors still match spec/ verbatim and each occurs exactly once, re-verified mechanically on 2026-09-09.** The only multi-occurrence one is the §29.4 step-13 anchor `([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).`, which occurs THREE times in spec/29 (:645, :711, :982) and resolves only through the instruction's "In §29.4's numbered step 13" scoping. Do not re-run this sweep; it is now confirmed by roughly twenty agents.

FACT: **`exited_cleanly` is defined in exactly one file in the whole repository's contract surface: `schemas/lenny-adapter.proto:1666`.** `grep -rn "exited_cleanly\|ExitedCleanly" spec/ docs/ charts/` returns nothing, so CODE-1's re-keying of that field owes no spec or docs edit and the whole obligation is the standing R1b Deferred.

FACT: **`RuntimeOps.Terminate` does not block on `deadlineMs`; it writes one frame and returns.** This is what makes CODE-4's budget/2 split actually hold: the adapter spends the graceful window only inside `Runtime.Close` (`contextWithGraceDeadline`), so `drain + close` cannot consume the whole RPC budget and the gateway still reads the reclaim's real outcome. A future round re-deriving the "the RPC deadline is also the adapter's grace" finding should stop here. EVIDENCE: pkg/adapter/runtimeops.go:486-491; pkg/adapter/session.go:299-307,:323-332.

FACT: **`Client.Shutdown`'s fourth parameter is a `time.Duration`, not milliseconds**, so CODE-4's `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", budget/2)` compiles as written and `int32(deadline.Milliseconds())` is applied inside. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-819.

FACT: **`resumeOnPod` has every collaborator CODE-5's third caller needs in scope.** `match` (from `podsession.ResolvePool` at start.go:3983) carries `.Pool` and `.MaxConcurrentSessions`; `maxConcurrentSessions()` is already applied at :4029; and `s.slotHealth`, `s.slotStates`, `s.slotReplacement`, `s.slotLeakGauge`, `s.podBinder` all exist on the Server (sessionserver.go:188,:406,:411,:416,:421). The `podBinder.Resume` failure branch the proposal cites as `:4041` is at :4040-4043. Nothing about that caller is speculative.

FACT: **The per-slot sub-state machine has exactly THREE mirrors outside spec/06, and all three are staged.** `docs/reference/state-machines.md:234-237` (DOCS-1), `pkg/sandbox/slotstate` (CODE-3), and the tier-11 gate `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`. `grep -rn "slot_cleanup\|slotCleanup" schemas/ charts/ docs/ spec/` finds no fourth: the mermaid blocks at docs/reference/state-machines.md:149-151,:175-176 are SESSION states in a different vocabulary, `docs/runtime-author-guide/lifecycle.md:34-52` is the pod-level table, and `schemas/lenny-adapter.proto:442` is a `// spec:` comment on `SessionScrubOutcome` that names `slot_cleanup → released` and is untouched by SPEC-4.

WATCHOUT: **`docs/api/internal.md` still names a `StopSession` RPC the protocol does not declare** (in the architecture-diagram alt text and its ASCII fallback). It is unrelated to 0081 and is NOT an edit site for it, but an agent grepping the docs corpus for teardown RPCs will land on it and may mistake it for drift this proposal creates. EVIDENCE: docs/api/internal.md:18-27.

USEFUL [Standing context → Traps]: the trap block saved a full round. Five of my candidates (`adapter-contract.md:75`, the two `leaked` glosses, the `slot_cleanup ──→ released` gloss, the `concurrent_slots_exhausted` gloss, and the "adapter cannot see this leak" line) were already built, priced and killed there with the reasoning intact, so I could drop each in one read instead of one verification pair.


### [non-spec.2.review-feasibility.1]

DECISION: returned an EMPTY findings list — BECAUSE every actor the staging names exists under that
name and can perform the action assigned to it, every collaborator the new call sites need is in
scope at those sites, and the three candidates I built all reduce to classes this loop has already
refuted. ALTERNATIVES: the `budget/2` graceful window against §5.2's stated 5s adapter minimum (see
MISTAKE below), the §7.2 step-3 / §6.2 cancel-bullet actor question (standing Trap, four lenses), and
the podclaim tier label (standing Trap #348).

FACT: the newest text in the staging is CODE-4's `budget/2` graceful-window split, and every citation
in it resolves exactly. `Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)` is at
`pkg/gateway/runtime/adapterclient/client.go:807`, so `cl.Shutdown(rctx, req.SessionID,
"slot_bind_failed", budget/2)` type-checks; `userTerminateDeadline = 10 * time.Second` and
`userTerminateRPCTimeout = 20 * time.Second` are at `cmd/lenny-gateway/user_revocation.go:50,:55` with
the call at `:128-129`, so the "§11.4 holds the same relation" claim is literally a 2:1 ratio;
`drainReason` is `pkg/adapter/session.go:314-321`; `drainViaLifecycle(req.GetDeadlineMs(), ...)` is at
`:260`; the `terminate` frame's `deadlineMs` `minimum: 100` and its `required` list are
`schemas/runtime-ops-events.schema.json:180,:183`.

FACT: the budget split cannot self-defeat through the drain, and here is why, because it is the first
thing a reader worries about. `RuntimeOps.Terminate` only writes a frame
(`pkg/adapter/runtimeops.go:486-491`), so it does not consume any of `deadlineMs`. The handler's only
timed work is `Runtime.Close` under `contextWithGraceDeadline(ctx, deadlineMs)`
(`pkg/adapter/session.go:262-266`, helper at `:327-332`), so handler wall time is roughly `budget/2`
against an RPC deadline of `budget`. The gateway does observe the real outcome.

FACT: no adapter code enforces §5.2's "minimum 5s enforced at runtime by the adapter"
(`spec/05_runtime-registry-and-pool-model.md:545`). `resolveShutdownGrace`
(`pkg/adapter/mcpruntime.go:312-324`) takes `time.Until(ctx.Deadline())` verbatim with no floor, and
`SocketRuntimeProcess.Close` calls it with `configured=0` (`pkg/adapter/socketruntime.go:456`). So a
`deadline_ms` of 2500 is NOT clamped back up to 5000, which is what would have made the split
pointless. Anyone re-deriving the split's soundness needs this fact.

MISTAKE (nearly filed, and here is what kills it): "`budget/2` puts the adapter's graceful window at
2500ms on a floor pool, below §5.2's stated 5s per-slot cleanup minimum, which SPEC-3 leaves standing
and whose bullet SPEC-3's pointer clause makes govern this very cleanup." The two quantities are
different: §5.2's cleanup timeout bounds the whole slot cleanup, while `deadline_ms` bounds only the
runtime close's graceful phase (`client.go:795-799`, "deadline bounds the graceful phase"). The
shipped ordinary release passes `0` (`podsession/slotbinder.go:542`), so no spec sentence fixes a
value for this field on the per-slot path, and the proposal argues the choice explicitly at
non-spec-changes.md ("the shipped §11.4 revoke fan-out pins ten seconds irrespective of the pool's
configuration"). Preference between workable designs; it would be refuted.

FACT: every collaborator CODE-5's third `accountSlotFailure` caller needs is in scope at
`resumeOnPod`'s `podBinder.Resume` failure branch. `match` is the local built at
`pkg/gateway/sessionserver/start.go:3980-3984` and is live at the branch (`:4041-4043`);
`s.slotHealth`, `s.slotStates`, `s.slotReplacement` and `s.slotLeakGauge` are Server fields
(`sessionserver.go:406,:411,:416,:421`); `s.podBinder` is `*podsession.Binder` (`:188`), which
satisfies `slotBinder`'s three methods (`start.go:2725-2729`). No seam is missing.

FACT: `runWithQueue` is a free generic function (`pkg/gateway/sessionserver/queue.go:134`), not a
`*Server` method, so the staged tier-1 "exclusion survives a `queue`-pool re-entry" case can compose
it directly over `applySlotRetryPolicy`; `newPodClaimQueue(poll, clock)` is already driven that way at
`queue_edges_internal_test.go:42` and `queue_internal_test.go:95`. A lens that assumes the queue is
only reachable through the Server will wrongly call that case unbuildable.

FACT: `claimSessionSlot` has exactly three production callers and no fourth
(`pkg/adapter/session.go:111`, `resume.go:50`, `sdkwarm.go:217`), so `st.started` cannot be set by any
path that is not one of the three start RPCs §4.7's staged row names. That is what makes SPEC-1's
"a session whose start the adapter has admitted" implementable as `st.started` with no residual class.

FACT: `Binder.BindSlot` and `Binder.ClaimSlot` both reach `ClaimSlot` only through `connectSlot`
(`podsession/slotbinder.go:128`, `:157`), whose `podclaim.SlotRequest` literal at `:422-428` is the
single mapping site `ExcludePods` needs; `BindReservedSlot` reconnects and never claims
(`:230-254`), and `reserveResumeSlot` targets a named pod through `ReserveSlotOnPod`
(`binder.go:1683-1695`). One field, one mapping line, two `continue`s — the staged reach is exact.

FACT: `ClaimSlot`'s terminal error selection is `len(list.Items)==0 → ErrNoIdlePod`, else
`sawTenantMismatch → ErrTenantMismatch`, else `ErrNoConcurrentSlot`
(`podclaim/slotclaimer.go:511-521`). An `ExcludePods` skip empties the candidate set without touching
`sawTenantMismatch`, so the staged tier-2 "every remaining candidate is excluded" case really does
land on `ErrNoConcurrentSlot`. Do not re-derive.

USEFUL [Settled, "Tier 7a and tier 4 are external test packages"] and [Settled, "The harness's tier is
the test's DIRECTORY rather than its infrastructure"]: together they pre-killed two candidates (the
tier-7a cohort assertions and the podclaim tier label) before I spent time on either.

USEFUL [Traps, "Do not file the cross-replica actor problem in §7.2"]: this is the single most
tempting thing on the staging for an actor-action lens, because §7.2 step 3 and §6.2's cancel bullet
both assign the reclaim to "the gateway" on an edge a different HTTP request drives. The trap's answer
(step 1 already presumes the sequence's replica owns the in-flight RPCs) is correct and saved a round.

OPEN [nothing new]. Every actor-capability question I could form is already carried in `## Open` or
`### Traps`; I added none.


### [non-spec.2.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE a full fresh sweep of the spec staging, the
non-spec staging, the checklist and the summary, read as one document against the tree, turned up
nothing that clears the materiality bar and is not already refuted, barred by a standing Trap, or
carried as a recorded Open/Deferred — ALTERNATIVES: I built and dropped six candidates, each listed
below with what killed it, so a later lens does not rebuild them.

FACT: the whole `scratchpad/cp-snap/0081` directory is byte-identical to the live proposal except
`non-spec-recheck-2-r5` and older, and the only delta since r5 is two paragraphs: the 0078 impacts
row gained "it edits no file 0078's TEST-7 rewrites, so that last overlap is package co-location
rather than a file collision", and the problem statement's 0078 paragraph was rewritten off the
withdrawn widened-exposure premise. Both check out against 0078's own text (its fixed decision at
proposals/0078_fix_keep-the-pod-listener-across-a-session-teardown.md:46-48, and TEST-6/TEST-7's
file list at :784-785, where TEST-7 owns
`tests/tier7a_load_local/shutdown_drain_gate_race_test.go` — the file 0081 explicitly leaves
unedited). EVIDENCE: proposals/0078_fix_keep-the-pod-listener-across-a-session-teardown.md:46-48,:784-786

FACT: the `budget/2` graceful-window fix is sound end to end and its precedent citation is exact.
`Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)` puts the fourth argument on
the wire as `DeadlineMs: int32(deadline.Milliseconds())`, so `budget/2` is well-typed; the 5s budget
floor puts the value at ≥2500ms, above the `terminate` frame's `minimum: 100`; and the shipped §11.4
fan-out really does hold the same 2:1 relation (`userTerminateRPCTimeout = 20s` bounding the call,
`userTerminateDeadline = 10s` sent as the window). The adapter side needs no change: the shipped
`Shutdown` already does `contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)`
around `Runtime.Close`, and `SocketRuntimeProcess.Close` derives its SIGTERM→SIGKILL pivot from that
ctx. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:813-818;
schemas/runtime-ops-events.schema.json:180-183; cmd/lenny-gateway/user_revocation.go:50,:55,:128-129;
pkg/adapter/session.go:262-265; pkg/adapter/socketruntime.go:457-465

FACT: the `budget/2` split also silently repairs a pre-existing schema violation on this one path.
An ordinary teardown sends `deadline 0` (`Binder.ReleaseSlot` at slotbinder.go:542), which makes
`drainViaLifecycle` write a `terminate` frame with `deadlineMs: 0` against a schema whose minimum is
100 (standing Open "terminate frame deadlineMs"). The compensating reclaim now sends ≥2500, so it is
the one caller that satisfies the frame contract. Worth knowing before somebody "simplifies" the
fourth argument back to 0. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542;
pkg/adapter/session.go:260

FACT: no shipped gateway test goes red under CODE-4's compensation, and I checked the two ways it
could. (1) Every shipped `podsession` test that drives a failing bind sets `MaxConcurrentSessions: 8`,
so `slotCleanupBudget`'s division never sees a zero denominator; the one zero-valued
`SlotBindRequest` literal in the tree (`start_preclaim_internal_test.go:1021`) only feeds
`classifySlotBindFailure` and reaches no compensation. (2) `concurrentAdapter` defaults
`shutdownExitedCleanly: true`, so `TestBindReservedSlotReleasesReservationOnFailure_spec_5_2`'s
claim-deleted assertion still holds (`Leaked` false → `ReleaseSlot(..., false, false)`, today's
behaviour). `releaseCredentials` is nil-safe, so the wrapper's unconditional call cannot panic a
fixture with no credential service. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder_test.go:102,:409-436,:763-765,:813-815;
pkg/gateway/sessionserver/start_preclaim_internal_test.go:1018-1022;
pkg/gateway/podlifecycle/podsession/binder.go:1263-1268

FACT: `live ⊆ started` holds by construction, which is what makes CODE-1's `closeErr` meaningful at
the `if live` report. `st.sessionID` and `st.started` are set together in
`claimSessionSlotUnderLock`, and `noteRuntimeStarted` runs only after a successful claim, so a
session in `runtimeLive` always has `st.started` true on the entry `Shutdown` removes. A reviewer
worried that the report could fire with an unset `closeErr` can stop here.
EVIDENCE: pkg/adapter/slotsession.go:87-88; pkg/adapter/session.go:156-163

WATCHOUT: `noteRuntimeStartedLocked` carries an idempotency branch (`if s.cohortSession == sessionID`)
that keeps a repeat start from raising `runtimeCohort`. CODE-2 wraps `noteRuntimeStarted` and does
not touch the `Locked` form, so the behaviour survives, but the staged doc comment drops the sentence
that recorded it. That is the standing Open "CODE-2's replacement doc comment drops the shipped
idempotency sentence"; it is real and it is cosmetic. EVIDENCE: pkg/adapter/runtimegeneration.go:36-49

MISTAKE (mine, caught before filing): I nearly filed "`slotCleanupBudget` divides by
`maxConcurrentSessions` with no zero guard, so a fixture that leaves the field unset panics." The
Settled entry already names it, every production call site is concurrency-gated or normalised, and
every shipped and staged fixture sets the field. Filing it would have been a hypothetical-hardening
finding. Do not rebuild it.

MISTAKE (mine, caught before filing): I nearly filed "the compensating `Shutdown` is sent on the
exclusive `Binder.Resume` branch, where `reserveResumeSlot` returned `""` and the accounting is
skipped, so the reclaim's `Runtime.Close` can brick a replacement pod nothing retires." Every half of
it is already carried: the close-bricks-the-pod half is barred by the standing Trap on the reclaim's
`Runtime.Close` last-close branch, the pod-not-released half is the standing Open "Does CODE-4 owe a
pod release on the resume path?", and the accounting-skipped half is stated in CODE-5's own text.

MISTAKE (mine, caught before filing): I nearly filed "the compensation's RPC deadline still has no
margin over the adapter's TOTAL cleanup work, because `budget/2` bounds only the runtime close while
`emitFinalUsage`, the drain frame and `removeSlotTree` run outside it." The dominant term is the
close, the fix addresses it, and the residual is speculative arithmetic with no stated budget it
breaches. It is the standing Open "the reclaim's deadline has no margin"; the round narrowed that
Open rather than closing it.

MISTAKE (mine, caught before filing): I nearly filed the S2 checklist line for not naming SPEC-2's
§7.1 atomicity-parenthetical replacement among the edits it lands. It names the deliverable id, whose
body is authoritative, and this loop has now refuted five findings of that bookkeeping class on the
materiality bar.

FACT: three narrative-attribution slips survive that no lens has filed and that I judged below the
bar, recorded so a later round prices them once rather than re-deriving them. non-spec-changes.md:58
and :1095 both say "SPEC-2's edge-case list", but the edge-case list belongs to `spec-changes.md` as
a document rather than to deliverable SPEC-2, which stages no edge-case text. Loose attribution in
proposal prose, no applied consequence.

FACT (re-derivation nobody needs to repeat): every markdown anchor and every relative path in the
staged replacement blocks resolves from the file the block lands in — spec/04's `#47-runtime-adapter`
is intra-file, and each of the nine cross-file links carries the right sibling filename. Checked all
of them against the Settled anchor list; no drift.


### [non-spec.2.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE every kube surface the staged non-spec
lane touches is either read-only, already spec-sanctioned in §4.6.3, or a pre-existing
mechanism this proposal only adds a caller to; I re-derived each from the tree rather than
from the standing context. ALTERNATIVES: I built and dropped four candidates, each recorded
below with the fact that kills it, so a later kubernetes lens does not rebuild them.

FACT: `spec/18` needs no edit, and this now has a derivation rather than a grep. Its
phase-12c deliverable lines state the recycle and slot surfaces at the level of "the
per-session slot cleanup and whole-pod scrub split, the reserved-hold claim window, and the
retirement limits" and "the Redis slot-counter capacity gate and the
`acknowledgeProcessLevelIsolation` requirement". Neither enumerates a precondition, a report
rule, or a state-machine edge, so nothing SPEC-1 through SPEC-4 stages can make a phase
deliverable stale, and no deliverable of an earlier phase gains a dependency on a later one.
This closes standing Open "Does `spec/18` need an edit?". EVIDENCE: spec/18_build-sequence.md:531-533,:538

FACT: the §4.6.3 gateway grant covers every kube write the code lane performs, verbatim.
"`get`/`patch` on `Pods` in agent namespaces ... for the tenant-id label and the drain-request
annotation, `create`/`get`/`delete` on `SandboxClaim` resources for the per-pod claim
lifecycle, `get`/`patch` on the `sandboxclaims/status` subresource ... and `get`/`list` on
`Sandbox` resources for pod selection during claim. The gateway holds no `sandboxes/status`
grant". CODE-4 and CODE-5 add no new verb and no new resource: `DrainSandbox` is a
merge-patch of one Pod annotation under `client.FieldOwner(ownership.Gateway)`, and the only
claim writes on the changed paths are the shipped `DeleteClaim` and `WriteRecyclingStatus`
inside `SlotClaimer.ReleaseSlot`, neither of which the staging opens. EVIDENCE:
spec/04_system-components.md:633 (gateway ServiceAccount RBAC paragraph);
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:95-108; pkg/gateway/podlifecycle/podsession/slotbinder.go:601-603

FACT: `ExcludePods` cannot become a write. In `ClaimSlot` pass 1 the `expiredByUptime`
`continue` the proposal says to sit beside is at slotclaimer.go:447-455, which is BEFORE
`rebindReservedSlot` (:459-465, the one `reserved → bound` status patch in the loop) and
before `reserveSlot`. In pass 2 it is after the `podClaim` GET and before `reserveSlot`, which
is where the claim CREATE lives. So an exclusion placed at either site skips a candidate with
no kube write attempted, exactly as `expiredByUptime` does, and its doc comment's
"read-only placement filter; the gateway skips the pod without writing Sandbox.status" carries
over unchanged. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:417-465,:483-500

FACT: no stuck-finalizer footgun, and the chain is worth writing down once because it is four
files deep. Every Sandbox carries `lenny.dev/session-cleanup`, and the sandbox finalizer
reconciler holds it while `activeClaimReferences` finds any non-terminal `SandboxClaim` with
`spec.sandboxRef` equal to the Sandbox — a `leaked=true` release leaves exactly such a claim
at `bound`. The bound is the §4.6.1 orphan GC: `classify` returns `reclaimDrain` for a
`bound` claim aged past `claimOrphanTimeout` (default 5 minutes) and `reclaimByDraining`
drains the pod and then `Delete`s the claim, which releases the finalizer. The GC is gated on
`PodHasActiveSession(claim.Spec.SandboxRef) == false`, so on a co-tenanted pod the claim is
held while a sibling session lives; once the sibling ends, its own release finds
`remaining > 0` (the leak's occupancy is never decremented) and keeps the claim, and the GC
then reclaims it. Bounded on both arms. EVIDENCE: pkg/apis/lenny/v1alpha1/sandbox_types.go:11-17;
pkg/controller/sandbox/finalizer.go:60-84,:111-126; pkg/controller/warmpool/gc.go:27-34,:222-248,:271-290,:359-368;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:833-848

MISTAKE: the standing Open "Concurrent resume leaves a freshly claimed pod at occupancy 1"
records its own narrowing as "the pod is now counted toward the §5.2 threshold by CODE-5's
third `accountSlotFailure` caller and drained, so the hold is bounded by the replacement
trigger rather than permanent." The second half is false at `maxConcurrentSessions >= 3`.
`UnhealthyThreshold` is `(maxConcurrent+1)/2`, `slothealth.Tracker` counts per pod, and each
§7.3 retry claims a DIFFERENT fresh idle pod through `b.connect`, so no single replacement pod
ever accumulates two leaks and the trigger never crosses. At concurrency 4 the threshold is 2
and one leak per pod drains nothing. What IS true, and what actually bounds it, is the §4.6.1
orphan GC at 5 minutes: `PodHasActiveSession` is false on a freshly claimed replacement pod
(`bumpRecoveryGeneration` runs only on success, so `row.PodAssignment` still names the OLD
pod), so predicate 1 fires and the claim is deleted. Re-point the Open at the GC rather than at
the trigger. The PROPOSAL is not wrong here — CODE-5's text says only that without the caller
the pod is "never counted unhealthy", and the summary's churn bullet scopes its drain claim
to `maxConcurrentSessions: 2` explicitly — so this is a log correction, not a finding.
EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:56-67,:215-221;
pkg/gateway/podlifecycle/podsession/binder.go:1591-1606; pkg/gateway/sessionserver/start.go:4041-4043,:4063-4067;
pkg/controller/warmpool/gc.go:271-290; non-spec-changes.md:619-621; summary.md (churn bullet, "at `maxConcurrentSessions: 2`")

WATCHOUT: three kubernetes-shaped candidates that look live and are not. (1) "The gateway
writing `SandboxClaim.status` is status-as-command-channel" — §4.6.3's ownership table makes
the gateway the sole owner of BOTH `spec.*` and `status.*` on that CRD and the
WarmPoolController consumes the binding state as projection input and "never writes them", so
it is a single-writer design by construction and no staged edit opens it. (2) "The
compensating `Shutdown` puts a controller on the hot path" — `DrainSandbox` stamps an
annotation and returns; nothing in the staged path waits on a reconcile, a work-queue, or
leader election, and the only blocking call is an adapter gRPC bounded by `slotCleanupBudget`.
(3) "`createClaimNeedsRollback` returns false for a `*SlotBindError`, so a `leaked=true`
`BindReservedSlot` release strands the create-time reservation with no rollback" — real and
deliberate; the Settled entry already adjudicates it as a held reservation rather than a
double-leak. EVIDENCE: spec/04_system-components.md:619-620 (ownership table row),:641 (claim
status enumeration paragraph); pkg/gateway/sessionserver/start.go:3211-3225

USEFUL [Standing context, "Ownership is clean"]: it named the exact shape to check
(no second field manager, no ForceOwnership, no finalizer) and it held on re-derivation, which
let me spend the round on the finalizer chain and the orphan-GC bound instead of re-walking
the ownership table. The one thing it does not say, and that the next kubernetes lens will
want, is that the finalizer exists and is claim-gated; that is in the FACT above.


### [non-spec.2.review-mechanism.1]

FACT: `diff -rq scratchpad/cp-snap/0081/non-spec-r2 proposals/0081_.../` is EMPTY. The round-1
non-spec fix pass changed nothing in the proposal, so the "read the changed sections first"
instruction had no changed sections to point at this round. Do not waste a call on the diff again
unless a later snapshot is named. EVIDENCE: scratchpad/cp-snap/0081/non-spec-r2 vs the proposal dir.

FACT: `stageWorkspace` CANNOT fail on a plan that carries no `uploadArchive`, `gitClone` or
`uploadFile` source. `rewriteExtractedSources` returns the plan unchanged when no source is
`uploadArchive`/`gitClone` (binder.go:1346-1354), the blob-fetch loop is entered only for
`uploadFileRefs(rewritten)` (:1296), and `PrepareWorkspace` is the LAST thing the function does
(:1324-1330). So every reachable `stageWorkspace` failure is a plan that DOES carry uploads.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1284-1332,:1346-1354,:1408-1420.

FACT: `FinalizeWorkspace` and `RunSetup` both call `ensureSlotPaths` at the top of the handler, so
either one creates the adapter's registry entry before it can fail. The only session-scoped RPC an
upload-free plan reaches first is `FinalizeWorkspace`, and it creates the entry.
EVIDENCE: pkg/adapter/staging.go:181,:337; pkg/adapter/slot.go:140-148.

MISTAKE: the standing Trap at review-log:349 ("The upload-free branch is listed separately only
because it is the one stage failure that sends no RPC at all") and the Settled fact at review-log:65
("an upload-free plan reaches `FinalizeWorkspace` as the first adapter RPC") contradict each other,
and the proposal followed the Trap. The Settled fact is the correct one. Filed as a finding this
round against non-spec-changes.md:493-498 and the tier-1 case at :847-850.

WATCHOUT: do NOT collapse that finding into the already-refuted item at review-log:375 (4)
("over-sending an obligation does not violate it"). That refutation GRANTED the premise that an
upload-free `stageWorkspace` failure exists and argued the extra send is harmless. The finding
attacks the premise itself, and its remedy is a different trigger description plus a buildable
tier-1 case. EVIDENCE: review-log.md:375.

FACT: on a solo-occupancy pod the compensating `Shutdown`'s `Runtime.Close` for a start parked
inside `SocketRuntimeProcess.Start` is a NO-OP, because `p.connected` is still false until the
accept returns (socketruntime.go:181-190,:435-438). The accepted-failure-mode bullet's claim that
"CODE-2's rollback `Runtime.Close` is the last close" is therefore correct, not an attribution
error. A round tempted to file that as a mis-attribution should stop here.
EVIDENCE: pkg/adapter/socketruntime.go:181-227,:435-467; non-spec-changes.md:1098-1104.

FACT: `slotCleanupBudget`'s `maxConcurrentSessions` divisor is never 0 on any reachable path
(`bindConcurrentSlot` is gated on `match.MaxConcurrentSessions > 1`; the resume path normalizes
through `maxConcurrentSessions(...)` at start.go:4029). The divide-by-zero worry is not filable.

FACT: every code citation in the CODE-1..CODE-5 blocks and the test-fixture paragraphs that this
lens opened resolves: session.go:133/147/156/157/163, resume.go:25-35/33/42/50/140/141/144,
sdkwarm.go:236/241/251/261/298, holdstate.go:251, slotfailure.go:41-48/74/91-99/100-101,
slotbinder.go:322-324, binder.go:293, start.go:2140/2172/2602/2606-2608/2720/2809/3246/4041,
queue.go:103-107/143-146/205, slotretry_test.go:69/383-407/468-492, slotretry_load_test.go:84-87,
export_test.go:45, usage_test.go:233/350-363, adapterevents_test.go:95/104-106/184,
podmcp_arming_internal_test.go:84/185/230, slotclaimer.go:830-836/845-847/881-885,
slotlayout/tree.go RemoveTree, tier11_docs test :32-37/:55/:70/:102-109,
podmcp_arming_handoff_test.go gatedRuntime :46-101. Do not re-verify this set.

FACT: `applySlotRetryPolicy` has 12 test call sites, not 13 (slotretry_test.go 11 +
slotretry_load_test.go 1). The proposal states no count now, so nothing to fix; recorded so a later
round does not re-derive it.


### [non-spec.2.review-operational.1]

DECISION: returned an empty findings list — BECAUSE every operational surface this lens owns
(metric emission, alert catalog, runbooks, condition writers, operator docs) is either untouched
by the staging or already adjudicated in the standing context — ALTERNATIVES: I built and dropped
five candidates, each listed below with what killed it, so nobody re-derives them.

FACT: the alert half of this lens is structurally inert and I re-verified it independently of
Settled #224. `grep -rn "lenny_slot_failure_total\|lenny_slot_pod_replacement_total\|leaked" pkg/alerting/rules/rules.go`
returns no alert on any slot metric, and `ls docs/runbooks/` carries no slot runbook. No alert
rule, no runbook, and no `tests/tier11_docs` alert-to-runbook edge is reachable from this
proposal. — EVIDENCE: pkg/alerting/rules/rules.go (no slot expr); docs/runbooks/ (no slot page)

FACT: `lenny_adapter_leaked_slots` IS named in `spec/` twice, at spec/05:545 and spec/06:160, so
SPEC-3's new clause naming the gauge introduces no unregistered metric reference. Its absence
from spec/16's inventory and from docs/reference/metrics.md is pre-existing and identical before
and after the staging. A finding shaped "SPEC-3 names a metric the observability inventory does
not carry" dies on spec/05:545. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545;
spec/06_warm-pod-model.md:160

FACT: the new `codes.Aborted` rollback status mints no metric label value. `lenny_slot_failure_total`'s
`error_type` comes only from the five closed stage constants passed to `recordSlotFailure`
(`slotFailureWorkspacePrep`, `slotFailureSetup`, `slotFailureCredentialAssignment`,
`slotFailureSessionStart`), never from a gRPC code or from `SlotBindError.Reason()`. This is the
same shape as Settled #168/Trap #371 for `Stage: "resume"`, re-derived from the emitter rather
than from the field. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:287,294,302,309,322,358-362

FACT: the withheld `ReportSessionScrub` reaches no observability surface at all.
`grep -rn "sessions_served\|sessionsServed\|served-session\|served_session" spec/16_observability.md docs/reference/metrics.md docs/runbooks/ pkg/alerting/rules/rules.go`
returns ZERO hits, so the served-session count the exception protects is carried by no metric row,
no alert, and no runbook. `lenny_pod_session_reuse_count` (spec/16:128, "observed per-pod at
session end") is the nearest row and the change only stops it counting a session the pod never
ran. — EVIDENCE: spec/16_observability.md:128; docs/reference/metrics.md:165

FACT (new, and it kills a plausible finding): shortening the adapter's graceful window to
`budget/2` cannot turn a clean reclaim into a `leaked` one. Neither runtime close reports the
SIGKILL pivot as an error: `SocketRuntimeProcess.Close` returns `p.listener.Close()` on both the
`cmd.Wait()` arm and the `time.After(grace)` kill arm, and `MCPRuntime.Close` returns `nil` on the
kill arm (it returns `exitErr(err)` only on the clean-wait arm). So `closeErr` is grace-independent
and `ExitedCleanly` is unaffected by the halving. I nearly filed "the budget/2 split makes a slow
runtime report unclean, so the fix inverts the defect it closed"; this is what killed it.
— EVIDENCE: pkg/adapter/socketruntime.go:435-467; pkg/adapter/mcpruntime.go:266-305

FACT: the newest fix (non-spec-changes.md:416-419, :429-446) verifies clean end to end. The client
signature really is `Shutdown(ctx, sessionID, reason string, deadline time.Duration)`, so
`budget/2` is type-correct (pkg/gateway/runtime/adapterclient/client.go:807-818); the adapter
really does pass `deadline_ms` straight into the frame at session.go:260; the schema really does
require `deadlineMs` with `minimum: 100` at schemas/runtime-ops-events.schema.json:180,:183 with
the reason enum at :181; and the §11.4 precedent really does hold the same 2:1 relation
(`userTerminateRPCTimeout = 20s` bounding the call, `userTerminateDeadline = 10s` sent as the
window, cmd/lenny-gateway/user_revocation.go:50,:55,:128-129). Every citation in that block
resolved. Do not re-verify it.

MISTAKE: none from an earlier round that this lens could find. Five candidates I built and dropped,
with the ground, so a later operational pass does not spend the round again:
(1) "SPEC-3 names a gauge the observability inventory does not carry" — dead, spec/05:545 already
names it. (2) "the budget/2 split misreports a slow runtime as leaked" — dead, both Close
implementations return nil on the kill arm. (3) "the reclaim's `terminate` frame carries
`reason: session_complete` for a bind that failed, misleading an operator" — this is the standing
Open at review-log:403 and the proposal answers it at non-spec-changes.md:429-435 on the shipped
`drainReason` default plus the §11.4 `USER_REVOKED` precedent; it is a re-scoped human question,
not a defect. (4) "CODE-5's new `DrainSandbox` callers need a `lenny_gateway_pod_retirement_total`
series" — dead, that counter's reasons are `session_count_limit` and `scrub_failure_limit` only
(spec/16:12); the unhealthy-threshold drain is counted by `lenny_slot_pod_replacement_total`
(spec/16:15), which the new callers do increment. (5) "the resume path trips the threshold with no
`lenny_slot_failure_total` series" — this is review-log:434 and it was FILED AND REFUTED by the
material skeptic in an earlier round; do not re-file it in any dress.

WATCHOUT: `docs/reference/adapter-contract.md:75` is the one docs site an operational or
docs-alignment lens will keep landing on, and it is BOTH a standing DEFERRED (review-log:463,
eleven lenses) and an explicitly REFUTED finding in the orchestrator's list. It is closed on the
refute side for this loop. — EVIDENCE: docs/reference/adapter-contract.md:75

UNVERIFIED: nothing new. The two operational Opens that remain (review-log:403 the `terminate`
reason value, review-log:434 the resume-path `error_type`) are both human/scope questions with
recorded grounds, not verification gaps.


### [non-spec.2.review-performance.1]

DECISION: returned an empty findings list — BECAUSE the capacity math on this change is net-negative
on every control-plane and data-plane store, and every failure-mode question my lens owns is either
already adjudicated in `## Standing context` or recorded in the proposal's own accepted-failure-modes
list. ALTERNATIVES: I built and dropped four candidates (below), each of which turned out to be a
close variant of something already refuted or an argument for a design change that belongs to a human
open decision.

FACT: the change is a net REDUCTION in control-plane writes, which is the opposite of what a
write-amplification lens expects and is worth stating once so nobody re-derives it. A `leaked=true`
release early-returns inside `SlotClaimer.ReleaseSlot` before the Redis DECR, before
`WriteRecyclingStatus`, and before `DeleteClaim`, so the compensating path issues FEWER etcd and Redis
writes than the shipped `ReleaseSlotReservation(..., false, false)` it replaces. The only net-new
control-plane write is `DrainSandbox`'s `lenny.dev/drain-request` annotation stamp on the two new
`accountSlotFailure` callers, which fires on a threshold crossing rather than per unit of work. No new
informer, no new watch, no new key, no new Lua script, no per-session write. EVIDENCE:
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836 (the leaked early return, verified this
round), :881-885 (the DeleteClaim it skips).

FACT: `slotCleanupBudget`'s denominator is safe in production and the reason is in the CALLERS, not in
the helper. `materializeSlot` has exactly two callers, `BindSlot` (slotbinder.go:133) and
`BindReservedSlot` (:254), both gated on `match.MaxConcurrentSessions > 1`; the resume caller reaches
it through `resumeOnPod`, which normalises with `maxConcurrentSessions(match.MaxConcurrentSessions)`
at pkg/gateway/sessionserver/start.go:4029. `ResumeRequest.MaxConcurrentSessions`'s own doc says
"normalized to a minimum of 1 by the caller" (podsession/binder.go:640-645). The exposed hazard is
test-side only: `pkg/gateway/podlifecycle/podsession/binder_test.go:492,:533` call `Binder.Resume`
directly with `MaxConcurrentSessions` unset, and :533 is the `cl.Resume`-failure branch CODE-4 puts
the compensation on. An implementor who lands CODE-4 without giving that case a non-zero concurrency
gets an integer-division panic rather than a failing assertion. Not filed (test fixture work inside a
file already on the edit list), but it is the one place the zero denominator is reachable.

FACT: the round's `budget/2` split checks out end to end against the tree, so the next reviewer need
not re-derive it. `adapterclient.Client.Shutdown`'s fourth parameter is a `time.Duration` converted to
`int32(deadline.Milliseconds())` (runtime/adapterclient/client.go:807,:817), so `budget/2` at the 5s
floor sends 2500 and clears the `terminate` frame's `"minimum": 100`
(schemas/runtime-ops-events.schema.json:180-183, verified). The adapter feeds that value into BOTH
`drainViaLifecycle` (pkg/adapter/session.go:260) and `contextWithGraceDeadline` for `Runtime.Close`
(:263-264), so the cited line is right and the "graceful window is what the frame carries" claim is
right. The §11.4 comparison is exact: `userTerminateRPCTimeout = 20s` bounds the call and
`userTerminateDeadline = 10s` is sent as the window (cmd/lenny-gateway/user_revocation.go:50,:55,
:128-129). The split also closes the standing Open on the `terminate` frame's missing `deadlineMs`
FOR THIS PATH ONLY: the shipped callers still pass 0 and still emit a frame with the required field
omitted, so that Open survives everywhere else.

MISTAKE (mine, nearly filed a fifth time): "the compensation's residual headroom is the wrong half".
With `budget/2` pinned as the adapter's runtime-close grace, the adapter's remaining work
(`emitFinalUsage`, the frame send, `removeSlotTree`'s four `os.RemoveAll`s, the response) has to fit
in the other `budget/2` — 2.5s at the floor — or the gateway's RPC deadline fires and a cleanup that
actually completed is classified `leaked`, which is exactly the harm the split was made to fix, moved
from the close to the tree removal. It dies on two grounds. Before the split `Runtime.Close` could
consume the WHOLE budget, so the split is strictly better rather than a regression; and on the main
residue class (bound-but-unstarted) `started` is false, no close runs at all, and the whole budget
goes to the tree removal. Asking for more headroom is hardening on a best-effort path whose failure
mode is the designed disposition.

MISTAKE (nearly filed): "the compensation blocks the client request goroutine for up to
`cleanupTimeoutSeconds`". Real and quantified — at the documented default `cleanupTimeoutSeconds: 60`
(docs/reference/configuration.md:99) a `maxConcurrentSessions: 2` pool gives a 30s block per failed
bind and `applySlotRetryPolicy` can pay it twice, and on an exclusive pool reached through
`Binder.Resume` the budget degenerates to the full 60s. It does not clear the bar: §16.5's session-
creation latency SLO is explicitly scoped to "receipt through `session_id` response ... the create
steps of §7.1" and excludes the bind, and the startup-latency P95 < 2s SLO is a P95 whose burn-rate
expression divides by `lenny_session_startup_duration_seconds_count` (pkg/alerting/rules/slo.go:223,
:226-227), so a bind-failure rate far below 5% cannot move it. Standing entries 216 and 342 reached
the same place; this round re-derived it from the alert rules rather than from the prose and agrees.

MISTAKE (nearly filed): "`lenny_adapter_leaked_slots` has an unbounded `pod_id` label and CODE-5 adds
two more producers of series that are zeroed rather than deleted". Verified true —
`applySlotRetryPolicy` ends its drain arm with `leakGauge(sbe.Pod, req.Pool, 0)` rather than a series
delete (pkg/gateway/sessionserver/start.go:2869-2871), and the gauge is registered with
`{pod_id, pool}` (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-225). It is
pre-existing cardinality growth and CODE-5 only widens the set of pods that reach an existing
emitter, which is the widening-of-pre-existing-looseness class this loop has refuted repeatedly.

USEFUL [Standing context, Settled 55/56/207/215/216, Traps 342/381]: the four capacity dresses the
round-2 performance lens already built and dropped (queue head-of-line blocking, pass-1 headroom
drain, the Redis-outage drain storm, the held connection) are all still dead on the current text, and
Settled 215's denominator note is what let me go straight to the caller list instead of re-deriving
the gate chain. This is the entry that saved the most time.

OPEN (unchanged, restated so it is not lost): the two live capacity questions on this proposal are
both about the THIRD `accountSlotFailure` caller rather than about the compensation — Open 427
(churn from accounting the §7.3 re-attach at all, whose recorded fallback is to account only the
leaked arm) and Open 428 (correlated re-attach churn at Tier 3, where a node loss puts every slot on
those pods into `resume_pending` and each failed restore now drains a freshly claimed replacement pod
at `maxConcurrentSessions: 2`). The proposal now states the per-instance change explicitly in its
accepted-failure-modes list, so what is left is an aggregate a human or a chaos-tier lens has to
price, not a defect a performance lens can file. I did not re-file either.


### [non-spec.2.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every reliability-lens candidate I built
either resolved against the tree, or landed inside one of the already-refuted families the
brief bars (the occupancy-hold family, the leaked-disposition family, the exclusive-pod
retirement family, `slotCleanupBudget`-has-no-test) — ALTERNATIVES: three candidates I built
and dropped are written out below so nobody rebuilds them.

FACT: the round's real delta is two paragraphs, not a staging rewrite. `diff -rq` over the
whole `scratchpad/cp-snap/0081` tree shows `non-spec-r2`, `non-spec-r2-start` and
`non-spec-r1-start` all byte-identical to the live proposal; the newest snapshot that differs
is `non-spec-recheck-2-r5`, and against it only `summary.md` (the 0078 impacts row gains one
clause about test-file co-location) and `problem-statement.md` (the 0078 sequencing paragraph
is rewritten to drop the withdrawn widened-exposure premise) changed. The `budget/2` split in
CODE-4 predates that snapshot. EVIDENCE: scratchpad/cp-snap/0081/non-spec-recheck-2-r5 vs the
live directory.

FACT: the `budget/2` fix is coherent end to end and I re-derived all four of its load-bearing
claims. `Client.Shutdown(ctx, sessionID, reason string, deadline time.Duration)` takes a
Duration, so `budget/2` type-checks, and `shutdown` writes `DeadlineMs: int32(deadline.Milliseconds())`
(pkg/gateway/runtime/adapterclient/client.go:807,:813-819). The §11.4 precedent is real and is
the same 2x ratio: `userTerminateDeadline = 10s`, `userTerminateRPCTimeout = 20s`, the RPC
bounded by the latter and the former passed as the graceful window
(cmd/lenny-gateway/user_revocation.go:50,:55,:128-129). The frame's schema requires
`deadlineMs` with `minimum: 100` (schemas/runtime-ops-events.schema.json:180-183), and the
budget's 5s floor puts `budget/2` at 2500ms. The adapter feeds `req.GetDeadlineMs()` into both
`drainViaLifecycle` and `contextWithGraceDeadline` for `Runtime.Close`
(pkg/adapter/session.go:259-263). Side effect worth knowing: on this path the change also
retires the shipped `deadlineMs: 0` frame hazard the standing Open records, because the
compensation is now the one `Shutdown` caller that pins a positive window.

FACT: `SlotBindRequest.CleanupTimeoutSeconds` is `int` and lives at the END of the struct
(pkg/gateway/podlifecycle/podsession/slotbinder.go:26-108, field at :108), not in `binder.go`
where the three `CleanupTimeoutSeconds int` hits at :478, :520 and :667 belong to
`BindRequest`, `LaunchRequest` and `ResumeRequest`. A grep of `slotbinder.go` alone for the
identifier returns the field but a grep scoped to the package misleads about which struct owns
which. `ResumeRequest.CleanupTimeoutSeconds` is populated on the resume path from
`match.CleanupTimeoutSeconds` (pkg/gateway/sessionserver/start.go:4037), so
`slotCleanupBudget` has real inputs on both paths.

FACT: `connectSlot` has NO Postgres fallback claim, unlike the whole-pod `connect`. `ClaimSlot`
is the single placement route on the bind path and its only two candidate loops are pass 1
(same-tenant claimed pods) and pass 2 (idle pods), so `ExcludePods` honoured in those two
loops cannot be bypassed by a third route. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:415-441 (no `fallbackClaim`);
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:415-505. Contrast
pkg/gateway/podlifecycle/podsession/binder.go:1745-1755, where `connect` DOES fall back.

MISTAKE (nearly filed, and here is what kills it): "the resume path's release runs on the dead
caller context, so `Leaked` is true even when the reclaim succeeded". The mechanism is real —
`compensateFailedSlotBind` detaches with `context.WithoutCancel` while `releaseResumeSlot` and
`applySlotRetryPolicy`'s `ReleaseSlotReservation` both ride the caller's ctx
(pkg/gateway/podlifecycle/podsession/binder.go:1705-1712;
pkg/gateway/sessionserver/start.go:2833), and the caller's ctx being dead IS the design's own
primary trigger. It does not clear the bar on three grounds. The leak is HONEST: the Redis
increment genuinely was not released, so `leaked` is the correct disposition and the drain the
correct reclaim. The stranding itself is pre-existing on every release site. And the proposal
states and tests the arm deliberately (non-spec-changes.md:614-618, :899-901). What is left is
"the release should be detached too", which is hardening on shipped code.

MISTAKE (nearly filed): "SPEC-2's §7.2 step 3 orders the reclaim before the replacement pod is
'released back to the pool', but on the `resuming → cancelled` edge step 1 has cancelled the
resume context by design, so the staged code's release deterministically fails and the pod is
held leaked rather than released". Killed by the standing Settled fact that §7.2 steps 1 and 3
have no implementation on the DELETE path, so the deterministic-cancellation premise is not
reachable in the tree and correcting step 3 creates no code obligation.

MISTAKE (nearly filed): "the adapter's remaining work after `Runtime.Close` (emitFinalUsage,
removeSlotTree, and on the `live` arm `reportSessionScrub`) can push the handler past the
gateway's `budget` even though the close is bounded at `budget/2`, so the doc comment's
'outlasts it' claim is only true of the close". True as stated, and immaterial: `emitFinalUsage`
is two calls with an early nil return (pkg/adapter/session.go:338-347), `reportSessionScrub`
logs rather than blocks on failure (pkg/adapter/sessionscrubreporter.go:61-85) and is only
reachable on the narrow arm where the adapter completed the start the gateway abandoned, and
the same unbounded tail already runs on every shipped session end with no budget at all.

UNVERIFIED: whether `SocketRuntimeProcess.Close` actually returns within the grace it is
handed, rather than merely sizing its SIGTERM/SIGKILL pivot from it. I read
`contextWithGraceDeadline` (pkg/adapter/session.go:323+) and the three `Close` implementations'
contract from the standing context but did not trace `resolveShutdownGrace`'s wait loop. The
`budget/2` split's whole margin argument rests on it. A code-lane reviewer with
socketruntime.go and mcpruntime.go open should confirm once and record it.

USEFUL [Standing context, Settled #205 and #211]: the three-leaked-discriminators entry and the
"every adapterclient RPC rides the caller's ctx" entry are what let me price the
detached-release candidate in one pass instead of rebuilding the whole discriminator. Together
they say `relErr` is always nil on the leaked arm (so the two-term discriminator never
double-counts) and that the release is already dead whenever the compensation is needed.

USEFUL [Standing context, Traps #339 and #341]: the sequential-reuse carve-out trap and the
"pass-2 exclusion is unreachable" trap each stopped a candidate of mine before I spent a
verifier pair on it.


### [non-spec.2.review-security.1]

DECISION: returned an empty findings list, second consecutive empty security pass —
BECAUSE the proposal text is byte-identical to what `non-spec.1.review-security.1` cleared
(`diff -rq scratchpad/cp-snap/0081/non-spec-r1-start proposals/0081_.../` is empty apart from
the review log), and every check I re-derived independently came back the same way.
ALTERNATIVES rejected: filing the round-1 WATCHOUT (the §11.4-revoke-versus-in-flight-start
window) as a finding — killed by the new FACT below, which makes it weaker still, not
stronger.

USEFUL [non-spec.1.review-security.1]: its five FACTs (credential-material improvement,
`Leaked`-as-self-report already shipped, tenant pinning surviving the leaked hold, the resume
path minting no lease, the hold-state allowlist) are all still true and saved me from
rebuilding four candidates. Its two WATCHOUTs are the only live security questions left and
both are correctly parked.

CORRECTS [non-spec.1.review-security.1]: its WATCHOUT overstates the reach of the
`reportSessionScrub` regating. A §11.4 full revoke cannot reach a bound-but-unstarted or a
started-but-not-`runtimeLive` session at all: `podTerminateFanOut.terminateLocal` iterates
`p.registry.Snapshot()` and skips any session with no binding in the per-replica
`podsession.Registry` (`cmd/lenny-gateway/user_revocation.go:110-137`), and a binding is only
registered after the bind returns, i.e. after `noteRuntimeStarted`. So the revoke-versus-start
interleaving the WATCHOUT names needs the session already registered, at which point `live` is
true and CODE-1 files the report exactly as today. The only caller that reaches the withheld
branches is the new compensating `Shutdown`, where today nothing is sent at all. A later round
wanting to revive that candidate now has to find a THIRD `Shutdown` caller, not just an
interleaving. EVIDENCE: cmd/lenny-gateway/user_revocation.go:110-137;
pkg/gateway/podlifecycle/podsession/slotbinder.go:528-545.

FACT: `removeSlotTree` cannot be widened into a cross-slot delete, and this is the check worth
doing on any future edit that moves the `bound` gate. It takes `st.paths`, a resolved
`slotlayout.SlotPaths` computed once at `ensureSlotStateLocked` from the map key, never from
`st.sessionID` (`pkg/adapter/slot.go:210-212`; `pkg/adapter/slotlayout/tree.go:59-71`). A
registered-but-unbound entry has an empty `st.sessionID` but fully resolved paths, and
`ValidateSlotID` refuses an empty id before an entry can exist, so CODE-1's move of the removal
from `bound` to `removed` cannot produce `RemoveAll("/workspace/slots")`. I built the
"empty session id collapses the path to the slots root and destroys every co-tenant's
workspace" candidate and it dies here. EVIDENCE: pkg/adapter/slot.go:112-126,:210-212;
pkg/adapter/slotlayout/slotlayout.go:108-120; pkg/adapter/slotlayout/tree.go:59-71.

FACT: the `ExcludePods` skip's position in `ClaimSlot` pass 1 is safe by construction, and the
line numbers to check are :428 (tenant check, sets `sawTenantMismatch`) and :433
(`expiredByUptime`, the precedent skip the proposal says `ExcludePods` sits beside). Because the
exclusion lands AFTER the tenant check, an excluded pod can never suppress an
`ErrTenantMismatch` that a fall-through depends on, and pass 2 skips any pod with a live claim
(:485-491) so a leaked-hold pod is already out of the idle scan. EVIDENCE:
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:428-441,:478-491,:505-522.

FACT: `deregisterSlotLocked` does not touch `runtimeLive`, so CODE-1's
`live := removed && s.runtimeHoldsLocked(sessionID)` read taken AFTER the deregistration is
meaningful rather than always false. Only `noteRuntimeStartedLocked` and `noteRuntimeClosed`
write that map. A reviewer who assumes the deregistration clears the cohort will file a
phantom "the report can never fire" finding. EVIDENCE: pkg/adapter/slotsession.go:174-188;
pkg/adapter/runtimegeneration.go:36-69.

FACT: `emitFinalUsage`'s move from the `bound` branch to the `started` branch is not a budget
or audit regression, and the reason is not "a zero report is harmless". It is that the only
caller that can now reach a bound-but-unstarted entry is the new compensating `Shutdown`,
which today sends nothing at all; `Binder.ReleaseSlot`'s end-of-session `Shutdown` names a
registered (therefore started) session, and the §11.4 fan-out cannot reach one (see the
CORRECTS above). EVIDENCE: pkg/adapter/session.go:243-250,:338-347;
pkg/gateway/podlifecycle/podsession/slotbinder.go:528-545.

WATCHOUT: the honest residual under lens check (2) is that a COMPROMISED adapter answering
`exited_cleanly: true` on a reclaim it never ran now buys three things it did not buy before:
the Redis decrement, exemption from the persistent leak count, and exemption from
`ExcludePods`. It is not a finding, and the reason is narrow: each of the three is strictly
more permissive than TODAY only if today were stricter, and today is not — today's
`ReleaseSlotReservation` hard-codes `leaked=false` (decrements unconditionally), the reserved
and resume paths reach no tracker at all, and no exclusion exists. Every new consumer is
therefore a tightening a lying adapter merely declines to opt into. A later round wanting this
needs to show a path where the lie makes the gateway do something it does NOT do today, not
merely fail to do something new. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:487-503,:542-543;
pkg/gateway/sessionserver/start.go:2594-2605.

UNVERIFIED (inherited, still open): `spec/05_runtime-registry-and-pool-model.md:545`'s
process-group-kill clause, which SPEC-3 extends to a new case, appears to have no
implementation anywhere in the slot-cleanup path. I re-grepped and agree with round 1. It is a
tree conformance gap rather than an 0081 defect; a conformance lens should own it.


### [non-spec.2.review-test-coverage.1]

DECISION: returned an EMPTY findings list — BECAUSE I walked every staged behaviour in CODE-1
through CODE-5, DOCS-1 and SPEC-4 against `## Testing` and each one has a named, concrete,
non-happy-path case at a tier the change actually reaches. ALTERNATIVES considered and rejected
below, each with the reason that killed it, so nobody re-derives them.

FACT: the round-2 delta is tiny and test-relevant in exactly one place. `diff -rq` over the whole
`scratchpad/cp-snap/0081` tree shows `non-spec-r2` byte-identical to the live proposal; the newest
snapshot that differs at all is `non-spec-recheck-2-r5` (summary.md + problem-statement.md only, the
0078 impacts/sequencing rewording, no test content). The last real `.non-spec-changes.md` delta is
`non-spec-recheck-2-r2` → live: the `budget/2` graceful-window split (CODE-4) plus the ONE Testing
edit it forced, the per-stage compensation table moving from "`deadlineMs` zero" to "a positive
`deadlineMs` equal to half the budget the case's pool configuration produces and strictly less than
the RPC deadline that same configuration produces". EVIDENCE: non-spec-changes.md:840-846.

FACT: that new assertion is testable as written and pins the whole point of the split. The fourth
argument of `adapterclient.Client.Shutdown` is a `time.Duration` (client.go:807-819, converted with
`int32(deadline.Milliseconds())`), so `budget/2` is type- and unit-correct; `concurrentAdapter` is a
real gRPC fake served over the package's bufconn (slotbinder_test.go:65-155), so an incoming ctx
deadline is observable if the fixture records it. Do not file "the budget arithmetic has no test"
again: it is now pinned through the deadline the compensation sends.

MISTAKE (nearly filed, five dresses, each dead for a stated reason):
(1) "The compensating reclaim of a STARTED session now emits a `terminate` frame carrying a non-zero
`deadlineMs` and nothing at any tier asserts it." Dead: `cmd/lenny-gateway/user_revocation.go:129`
already sends `userTerminateDeadline` (10s) on the shipped §11.4 revoke, so a non-zero deadline on
that frame is NOT new, and trap-383 item 2 already refuted the sibling `session_complete` framing.
(2) "Tier 3 is reached, because CODE-2 makes `StartSession`/`Resume` answer a new gRPC code." Dead:
the coupling is pinned from both ends at tier 1 (the adapter rollback case asserts `Aborted`; the two
`slotretry_test.go` table rows assert the classifier's mapping of that code), which is how this repo
pins classifier couplings, and standing-context Settled 192 already verified `Aborted` survives the
client and `slotErrCode`'s `errors.As` walk unmodified.
(3) "Tier 10 is reached, because CODE-1 changes when `ReportSessionScrub` fires and
`tests/tier10_conformance/recycle_scrub_conformance_test.go` is the battery for that." Dead: an
earlier lens already declined it (review-log-archive:7180, "the tier-9 analogue was already refuted
and tier 10's adapter cases all drive started sessions") and Settled 222 re-derived the green.
(4) "The `SlotBindRequest.ExcludePods` → `podclaim.SlotRequest.ExcludePods` mapping in `connectSlot`
is asserted by nothing: tier 1 stops at the fake `BindSlot`, tier 2 starts at `ClaimSlot`, and the
tier-4 clause that would join them runs on a fixture that (standing Open) has neither a second
Sandbox nor a wired session server." Dead: this is the load-bearing half of an ALREADY-REFUTED
finding ("The staged tier-4 datastore-crossing case asserts a re-bind on a second pod its named
fixture does not have"), whose refuter held explicitly that the tier-1 write side plus the tier-2
read side are the coverage `.claude/rules/test-coverage.md` mandates.
(5) "CODE-2's `sdkwarm.go:261` arm now silently refuses a record with no rollback and no test." Dead:
the refusal is the fail-safe direction, the proposal's scope argument (only caller is
`Binder.Launch`, whose `failPhase` retires the pod) is a Settled fact, and asking for a case there is
an additional nice-to-have.

FACT: the adapter tier-1 cases are annotated `// spec: §4.7; §5.2` (non-spec-changes.md:748) even
though two of them ("Start-versus-reclaim rollback, deterministic form", "The rollback destroys no
successor") exercise staged §7.1's reclaim-versus-start race, whose own code comment cites
`// spec: §7.1 … §15.4.3` (:227). Deliberately NOT filed: §7.1 is not orphaned, because the gateway
tier-1 block carries `// spec: §7.1; §5.2; §6.2` (:838), so the harness's section→test map is
populated for it. A future annotation-tightening pass could add §7.1 to the adapter block; it is a
one-token edit and not a coverage gap.

WATCHOUT: the tier-11 line/range citations in the Testing section have drifted by one or two lines
and are NOT worth a finding — `generalSlotEdges` is at :32-37 (the proposal says `:32-36`) and the
doc-side `requireAllContain` list is at :103-110 (the proposal says `:102-108`). The cited ranges sit
inside or adjacent to the real ones, which is the same "citation ranges to normalise, not to file"
class the standing Traps already record. EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:103-110.

USEFUL [standing context, Traps]: the "MISTAKE nearly filed, four test-coverage dresses" entry and
the `fakeSlotBinder`-needs-a-recorded-requests-field entry (Traps) between them killed three
candidates before I spent a verifier pair on any of them. The Settled entry on directory-based tiers
(Settled 182) is what stopped me filing a `// diagnosis:` gap against the `pkg/`-resident cases.

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
