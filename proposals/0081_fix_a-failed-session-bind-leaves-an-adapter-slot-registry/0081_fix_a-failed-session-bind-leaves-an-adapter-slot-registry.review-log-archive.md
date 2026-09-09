# Review log archive — 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry

Entries the compaction pass has already curated. Its residue is
in the review log's standing context; this file is the record, for a
human chasing a citation. No agent reads it.

### Pass 1 — spec loop, round 1

The round-1 fixer recorded no pass subsection, so this one opens it and carries that pass's
corrections. The bullets below are follow-up corrections to round 1 rather than a new round.

- **Running boundary stated one way.** The staged `receiving_uploads → slot_cleanup`
  annotation, its transcription in CODE-3's doc comment, and its DOCS-1 table row all keyed on
  the session being dispatched to the runtime, which put a `StartSession` still in flight on
  the `running` side while the SPEC-4 prose and the Design paragraph put it on the
  `receiving_uploads` side. All four now state the boundary as the runtime having been given
  the session, with a start still in flight named on the `receiving_uploads` side, and the
  SPEC-4 prose and the Design paragraph say that this is the moment §6.2's existing
  `receiving_uploads ──→ running` trigger names, so the fence carries one trigger for the
  window and the untouched `running` edge and its docs mirror stay as written
  (spec-changes.md SPEC-4 fence, SPEC-4 prose, Design; non-spec-changes.md CODE-3, DOCS-1).
  Verified: spec/06_warm-pod-model.md §6.2 fence (`receiving_uploads ──→ running`, "workspace
  ready, session dispatched to runtime with its session identifier"),
  docs/reference/state-machines.md per-slot sub-state table, and
  tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go, which pins the edge
  names and the block scoping but not the trigger text.
- **The `leaked` disposition is scoped to the pod class that offers it.** The staged §7.1
  paragraph, its Design paragraph, and the edge-case bullet assigned an unacknowledged
  reclaim the `leaked` sub-state and the §5.2 whole-pod replacement trigger for a pod of
  either concurrency. §6.2 states `slot_cleanup ──→ leaked` under "Per-slot sub-states scoped
  to concurrent occupancy" and §5.2 states the replacement trigger under "Slot retry policy
  (`maxConcurrentSessions > 1`)", and
  tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go fails the build if the
  `leaked` edge leaves that block. All three sites now scope the disposition to a pod serving
  concurrent sessions and state what an exclusive pod does instead: the failed attempt
  releases the pod's claim and the pod retires under §6.2's pre-attached failure disposition,
  which is the problem statement's own finding that single-session pools are unaffected
  (verified at `pkg/gateway/podlifecycle/podsession/binder.go` `failPhase`, which drains the
  Sandbox, and `ReclaimClaimed`, which deletes the per-pod claim).
- **The disqualification names the mechanism that carries it on each path.** The §7.1
  paragraph obliged the reclaiming pod's disqualification for a §7.3 re-attach and then
  routed every retry through §5.2's slot retry policy, which no resume takes:
  `Binder.Resume` is called from `resumeOnPod` with a `ResumeRequest` and selects its pod
  through `b.connect(ctx, req.Pool, …)`, and the staged `ExcludePod` field is set only in
  `applySlotRetryPolicy`. The paragraph, the Design paragraph, and the edge-case bullet now
  state the disqualification per attempt kind: §5.2's slot retry policy places the retry of an
  attempt it governs on a different pod, and a §7.3 re-attach claims its replacement pod from
  the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod
  outside. Verified: `podclaim.SlotClaimer.ReleaseSlot` returns before the counter decrement
  and before the claim DELETE on a leaked release, so the pod keeps its per-pod claim and its
  occupancy, and `podclaim.Claimer.Claim` skips any Sandbox whose phase is not `Idle` and
  guards acquisition on the per-pod claim CREATE. No staged deliverable changes, so the
  `ExcludePod` block and Files touched in the non-spec changes stand as written.

DEFERRED (out of bounds for this lane — the implementation checklist is not writable here;
each is a checklist line that states a predicate the staged text has withdrawn):

- DEFERRED · implementation-checklist.md S7 states the runtime teardown gate as
  runtime-cohort membership and names no report gate. The staged §4.7 row and CODE-1 gate the
  teardown on the admitted start (`st.started`) and the cleanup-outcome report on
  `runtimeLive` membership (`runtimeHoldsLocked`). S7's reach should also name
  `pkg/adapter/runtimegeneration.go`, whose accessor CODE-1 is the first caller of.
- DEFERRED · implementation-checklist.md S3 names only the `**Slot cleanup:**` bullet. SPEC-3
  now stages two §5.2 anchors: the bullet's action-list sentence (the credential directory and
  the §4.9 timer cancellation) and the `**Scrub model.**` paragraph (the pre-`running`
  cleanup, the withheld report, and the one-report rule).
- DEFERRED · implementation-checklist.md S2 and S10 carry a no-retry rule the staged text no
  longer contains, and S2 names the atomicity paragraph rather than the new §7.1 paragraph and
  omits the §7.3, §6.2 and §5.2 `**Max retries:**` anchors SPEC-2 now stages. S10 omits the
  `ExcludePod` deliverable, `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, and tier 2.
- DEFERRED · implementation-checklist.md S8 requires `StartSession` to report the racing
  reclaim as leaked. The staged §7.1 text and CODE-2 file no cleanup-outcome report on that
  path, and the `errStartRaceReclaimed` sentinel no longer appears anywhere in the proposal.

### [spec.1.fix-G1.1]

DECISION: The runtime teardown's precondition in §4.1, the §4.7 `Shutdown` row and Design paragraph one is now "a session whose `StartSession` the adapter has admitted", and the phrase "the pod's shared runtime process has been given" is kept only where it denotes `runtimeLive` cohort membership (§6.2's `running`, the served-session count, the §4.7 item 7 exactly-one-session rule) — BECAUSE the phrase is already bound to that one referent at spec/04_system-components.md:962, spec/15_external-api-surface.md:1734, spec/28_communication-channels.md:1242 and :1735, spec/29_communication-scenarios.md:308, and pkg/adapter/runtimegeneration.go:21-25 defines it verbatim, while the staged code gates the teardown on `st.started`, set at pkg/adapter/slotsession.go:87-89 inside `claimSessionSlotUnderLock` before `Runtime.Start` — ALTERNATIVES: moving the code to `runtimeLive` (fails open in the start-in-flight window, which is the third residue class); glossing the phrase with a second meaning inside §4.7 (one phrase, two referents, in the same section); making teardown, report and `running` share one predicate (puts the report on `started`, which contradicts SPEC-3's own no-report exception and double-advances the served-session count).

DECISION: I wrote "taken from the moment the adapter admits the start" where the design's prescribed text said "from the claim that admits the start" — BECAUSE "claim" in this specification denotes the `SandboxClaim` CRD (spec/06_warm-pod-model.md `reserved` hold semantics, spec/04 §4.6.1), and a reader of §4.7 would resolve it there rather than to the adapter's slot claim. Same referent, no vocabulary collision.

DECISION: §5.2's `**Slot cleanup:**` action list is widened (SPEC-3's first anchor) rather than the §4.7 row enumerating the slot release's actions — BECAUSE §5.2 is the only action list on the surface and a second copy drifts, and because §5.2 already assumes the wider list ninety lines earlier at spec/05_runtime-registry-and-pool-model.md:455 ("after every ended session's per-slot tree and credential lease have been removed"). ALTERNATIVES: weakening the Design paragraph to claim only the tree and the `slotId` (abandons Goal 1); a disarm rule in §4.9 beside the arming paragraph (splits one cleanup's actions across two sections); scoping the widened actions to the new pre-start reclaim (a conditional for a rule that already holds unconditionally).

FACT: The widened §5.2 actions are shipped behaviour, so no code deliverable changes: `slotlayout.RemoveTree` already sweeps `p.CredentialsDir` — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68 — and `deregisterSlotLocked` already cancels every armed expiry timer on removal — EVIDENCE: pkg/adapter/slotsession.go:174-188.

FACT: `{Running, SlotCleanup}` is already in the canonical per-slot edge list, so a session the runtime was given needs no new edge and CODE-3 stays the single added edge — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:105-113.

FACT: §6.2's fence splits its per-slot edges into two blocks. `slot_cleanup ──→ leaked` sits in the concurrency-scoped block and `slot_cleanup ──→ released` in the either-concurrency block, which is why SPEC-4's prose must not re-enumerate the terminals — EVIDENCE: spec/06_warm-pod-model.md:146-156.

DECISION: The staged §6.2 fence annotation for the new edge ("the bind is abandoned or fails before the session is dispatched to the runtime") is left as staged, even though the prose paragraph now names a `StartSession` whose dispatch has not returned — BECAUSE it mirrors the pre-existing `receiving_uploads ──→ running` annotation's vocabulary at spec/06_warm-pod-model.md:152-153, which the design ruled stays true under either edge of the in-flight window, and rewording it would cascade into DOCS-1's table row and CODE-3's doc comment in the non-spec staging, which no lane owns this round.

WATCHOUT: SPEC-3's closing note still reads "Nothing else in the bullet changes: the action list ... stand as written", which now contradicts SPEC-3's own first anchor. The G1 design assigned that note, the "Spec files touched" spec/05 entry ("one sentence"), summary.md's What-changes §5.2 bullet and the SPEC-3 deliverable-index line to G2 in the same round, so G1 left all four alone deliberately. If G2's edit did not land, these four are the drift to close first — EVIDENCE: 0081...spec-changes.md SPEC-3 closing note and the `Spec files touched` spec/05 entry.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S7 still reads "runs the runtime teardown only for a session the shared runtime process has been given". What is true after this round: S7 delivers CODE-1 as "releases the slot for any entry the call removed, runs the runtime teardown only for a session whose `StartSession` the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given". The checklist is out of bounds for both fixers under the round's hard constraint.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S3 describes SPEC-3 as only the bind-abandoned coverage and the no-report rule. What is true after this round: SPEC-3 has two anchors in §5.2's `**Slot cleanup:**` bullet, the first widening the action list to name the slot's credential directory and the cancellation of that session's armed §4.9 direct-delivery-mode lease-expiry timers, the second inserting the pre-start coverage and its no-report exception.

UNVERIFIED: the non-spec CODE-1 code comment at non-spec-changes.md:72-77 still carries "the runtime teardown runs for a session the pod's shared runtime process has been given" for a gate that reads `st.started`. G1's design assigned that block's rewrite to G2 so it is not edited twice. Whoever reconciles the round should confirm G2 landed it.

### [spec.1.fix-G2.1]

DECISION: The withheld-report rule and a new one-report-per-session rule are stated once, appended to spec/05 §5.2's `**Scrub model.**` paragraph, against §6.2's `running` boundary — BECAUSE that paragraph opens "The scrub is uniform across session-mode configurations" and is the only concurrency-independent statement of the per-slot cleanup in §5.2; SPEC-2 and SPEC-4 then cite it rather than each stating their own version — ALTERNATIVES: keeping the rule in the `**Slot cleanup:**` bullet (rejected: the bullet sits under a heading scoped to `maxConcurrentSessions > 1`, so on a single-session pod §4.7's unqualified `ReportSessionScrub` row would govern instead); duplicating it into both paragraphs (rejected: two statements of one rule in one section is a drift generator).
DECISION: The refused `StartSession` reports no cleanup outcome, and CODE-2's second `reportSessionScrub` call plus the `errStartRaceReclaimed` sentinel are deleted — BECAUSE the adapter is the only place the at-most-one invariant can hold; `RecordSessionScrub` increments `sessions_served` before it branches on the leak flag and carries no per-session dedup — ALTERNATIVES: the reviewer's preferred "reclaim reports, refused start does not" (rejected by the design: the reclaim's teardown branch is gated on `st.started`, which is set at the claim, so the reporting reclaim would be exactly the pre-`running` cleanup SPEC-3 forbids from reporting); staging a §4.7 row exemption so a `leaked` report from a refused start does not increment `sessionsServed` (rejected: needs a wire distinction and would open schemas/lenny-adapter.proto, barred by programme rule S-2).
DECISION: The cleanup-outcome report gates on `runtimeLive` membership through a new `runtimeHoldsLocked` accessor, while the runtime teardown keeps its `st.started` gate — BECAUSE `runtimeLive` is the tree's own record of the sessions the pod's one shared runtime process has been given, which is verbatim §6.2's `running`, and `st.started` is set before `Runtime.Start` so it means "a start was claimed"; the teardown must fail closed toward closing and the report toward not counting — ALTERNATIVES: one predicate for both (rejected: reports for a session the reclaim is about to take back off the runtime).

FACT: `runtimeLive`'s writers are exactly `noteRuntimeStartedLocked` and `noteRuntimeClosed`; there is no third — EVIDENCE: pkg/adapter/runtimegeneration.go:36-49, :58-68 (grep for `runtimeLive` across pkg/adapter returns only runtimegeneration.go and the server.go field declaration).
FACT: `st.started` is set inside `claimSessionSlotUnderLock` before `Runtime.Start` is ever called, so it is already true during the call whose success `noteRuntimeStarted` records — EVIDENCE: pkg/adapter/slotsession.go:87-88 vs pkg/adapter/session.go:156, :163.
FACT: The gateway's session-scrub handler increments `sessions_served` unconditionally before it branches on the leak flag and holds no per-session dedup, so a second report for one session double-advances `recycle.maxSessionsPerPod` retirement — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481.
FACT: `slot_cleanup ──→ leaked` and `running ──→ failed` sit in §6.2's concurrent-only per-slot block, three lines above the "a pod of either concurrency" block; docs/reference/state-machines.md:251 mirrors that scoping and a tier-11 test pins the pair — EVIDENCE: spec/06_warm-pod-model.md:146-148 vs :150; tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go.

WATCHOUT: `SocketRuntimeProcess.Close` is at pkg/adapter/socketruntime.go:435-467 and the listener is bound at :156-161. Several proposal citations still say `:441-467` and `:155-161`; those ranges are inside the real spans rather than plainly false, so they were left alone, but a future citation check should normalise them — EVIDENCE: pkg/adapter/socketruntime.go:435, :467, :156.
WATCHOUT: Do not re-gate the cleanup-outcome report on `st.started` to "simplify" the two predicates back into one. It reintroduces the double report through CODE-2's rollback and advances `sessions_served` for a session that never ran. The tier-1 "Claimed but not yet recorded" case exists to fail when that happens — EVIDENCE: proposals/0081_.../…non-spec-changes.md CODE-1 clause-two block.
WATCHOUT: Do not widen §6.2's fence to move `slot_cleanup ──→ leaked` into the either-concurrency block to make a prose terminal claim true. It changes shipped state-machine semantics, cascades into docs/reference/state-machines.md:251, and breaks the tier-11 matched-pair test. G1 removed the terminal claim from SPEC-4's prose instead, which is the smaller fix — EVIDENCE: spec/06_warm-pod-model.md:146-150.

MISTAKE: An earlier round stated the report rule in two places with opposite content: SPEC-2 had the racing `StartSession` report the reclaim outcome while SPEC-3 forbade any report for the same reclaim. It cost a full round, and the cost was structural rather than verbal: each block stated its own version of one rule instead of citing a single statement of it.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S3 (line 10) reads "SPEC-3. §5.2's slot-cleanup bullet covers a bind abandoned before its session reaches the runtime and states that the reclaim reports no cleanup outcome." That is now false in both halves. What is true: SPEC-3 lands two anchors in §5.2 — the `**Slot cleanup:**` bullet's action list gains the slot's credential directory and the §4.9 timer cancellation, and the `**Scrub model.**` paragraph covers the cleanup of a bind abandoned or failed before its slot reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release.
DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S8 (line 20) reads "…`StartSession` takes the session back off the runtime, reports the reclaim as leaked, and refuses the start when it did not." The report is gone. What is true: "`StartSession` takes the session back off the runtime and refuses the start when it did not, reporting no cleanup outcome."
DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S7 (line 18) describes CODE-1 as running "the runtime teardown only for a session the shared runtime process has been given". G1 already flagged this. It is now doubly wrong: the teardown runs for a session whose `StartSession` the adapter has admitted, and it is the cleanup-outcome report, not the teardown, that is owed only for a session the shared runtime process was given.

UNVERIFIED: The tier-1 fixtures named for the adapter cases (`slotPod`, `probeRuntime`, `recordingSessionScrubReporter`) were not opened this round, so whether a fixture can drive a start through `noteRuntimeStarted` without going through the exported `StartSession` is unchecked. The non-spec lane should confirm it when it writes the "Claimed but not yet recorded" case.

### [spec.1.fix-G3.1]

DECISION: Closed the §7.1-versus-§5.2/§6.2/§15.1 retry contradiction by replacing the no-retry clause with a pod-placement rule (the unacknowledged-reclaim pod is disqualified from carrying another attempt at the same session; the §5.2 retry itself is kept), and staged the mechanism as an `ExcludePod` field on `podsession.SlotBindRequest` and `podclaim.SlotRequest` read by `ClaimSlot`'s two candidate passes — BECAUSE nothing then becomes non-retryable, so §5.2's three non-retryable reasons, its Client-error-on-exhaustion disjunction, its Fresh workspace guarantee, §6.2's pre-attached retry policy and Client-visibility bullet, §15.1's error rows and docs/reference/error-catalog.md all stay true verbatim, and availability is not withdrawn during the partial outage that made the reclaim fail — ALTERNATIVES: the reviewer's suggested fix (a fourth non-retryable category mirrored across spec/05, spec/06, spec/15, spec/29 and three doc rows, plus a new SlotReason value so §5.2 has an `error.category` to set); deleting the clause and leaving the retry unfenced (the lagging `os.RemoveAll` hazard is real); a per-attempt fence on ShutdownRequest (blocked by programme rule S-2); draining the pod immediately (adds a second sufficient trigger beside §5.2's ceil(maxConcurrentSessions/2) threshold and retires a pod serving healthy co-tenants).

DECISION: Kept the resume compensation and gave it a basis by lifting the obligation out of the creation-atomicity paragraph into its own §7.1 paragraph scoped to any gateway bind attempt (creation finalize block, §15.1 start, §7.3 re-attach), with §7.3's resume flow and §6.2's mid-resume cancel edge as one-sentence pointers — BECAUSE the host paragraph is scoped by its heading to steps 2–8 and closes with a claim about a client that never receives a `session_id`, neither of which holds for a re-attach of a persisted session — ALTERNATIVES: widening the trigger in place (textually false at both ends of the host paragraph); restating the rule in §7.3 (two normative statements of one rule); dropping the resume compensation from CODE-4/S9 (the residue is real on the recovery path and it would falsify summary.md's recycle-boundary-sweep rationale).

WATCHOUT: §7.1's "**Atomicity of session creation (steps 2–8).**" paragraph physically sits INSIDE §7.1's fenced flow listing, between the step-8 line and the line continuing it, so any paragraph inserted after it also lands inside that fence. The staged SPEC-2 instruction now says so explicitly; do not "fix" it by moving the block below the fence, where it would sit among the `uploadToken` prose — EVIDENCE: spec/07_session-lifecycle.md:5 (fence opens), :23 (the paragraph), :24 ("(executionMode, isolationProfile, scrubPolicy summary)"), :54 (fence closes).

WATCHOUT: `Binder.DrainSandbox` only stamps the `lenny.dev/drain-request` annotation for the WarmPoolController; it removes nothing from placement synchronously, and `ClaimSlot`'s pass-1 scan of claimed pods reads the per-pod claim and never the Sandbox phase. A claim that "at maxConcurrentSessions 2 the drained pod is no longer a candidate" is wrong — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:601, pkg/gateway/podlifecycle/podclaim/slotclaimer.go:95-107, :417-469.

FACT: `ClaimSlot` returns `ErrNoConcurrentSlot` (which the binder maps to `WARM_POOL_EXHAUSTED` with `details.reason: "concurrent_slots_exhausted"`) whenever the pool list is non-empty and no candidate is placeable, so an exclusion that empties the candidate set needs no new sentinel and no new reason value — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:516-522, pkg/gateway/podlifecycle/podsession/slotbinder.go:430-441.

FACT: The two-pass placement-filter precedent for a read-only skip is `TestClaimSlotSkipsOverUptimeClaimedPod_spec_6_2` and `TestClaimSlotSkipsOverUptimeIdlePod_spec_6_2`, in an envtest-backed file, so a placement-filter case for `ExcludePod` is tier 2 rather than tier 1. `maxpoduptime_test.go` in the same package is fake-client tier 1 and is not the precedent to copy — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go:26,648,682; pkg/gateway/podlifecycle/podclaim/maxpoduptime_test.go:12.

FACT: `applySlotRetryPolicy` takes `req podsession.SlotBindRequest` by value, so setting `req.ExcludePod` on the retry iteration is scoped to that request's remaining attempts and reaches no other caller — EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884.

WATCHOUT: §5.2's gloss of `details.reason: "concurrent_slots_exhausted"` reads "pods exist but all slots are full", which does not literally describe a pool whose only candidate was disqualified by the §7.1 reclaim obligation. No edit is staged for that gloss (the group design bars minting a new reason value and did not adjudicate the gloss). A later round may judge the gloss a site — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:549.

UNVERIFIED: The staged tier-4 datastore-crossing case now asserts that the retry re-binds on a second pod, which requires `tests/tier4_integration/recycle_scrub_path_test.go`'s pool to carry a second placeable pod. Nobody has read that fixture; the implementor or the next non-spec loop should confirm the pool can be extended rather than assume it already has one.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S2 says "§7.1's atomicity paragraph gains the failed-bind pod-side reclaim obligation, its `leaked` disposition, the no-retry rule, and the racing-start rule; §4.7.9 step 5 points at it." False on three counts. True instead: §7.1 gains the obligation as a paragraph of its own inserted after the atomicity paragraph, covering the creation finalize block, the §15.1 start transition and the §7.3 re-attach; it carries the `leaked` disposition, the pod-disqualification rule (there is no no-retry rule) and the racing-start rule; §7.3's resume flow, §6.2's `resuming` mid-resume cancel bullet, §5.2's slot-retry `**Max retries:**` parenthetical and §4.7.9 step 5 are the pointer edits that land in the same step.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S10 says "a bind whose reclaim was not acknowledged is not retried" and names tiers 0, 1, 4. True instead: the retry iteration carries `ExcludePod` so `ClaimSlot` places it on a different pod, and CODE-5 now also touches `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, so the step reaches tier 2 (the envtest-backed placement-filter cases) beside 0, 1 and 4.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.summary.md]: the "Impacts on other proposals" row for 0080 §1.19 states the membership change of the sets `boundSlotState`/`checkSessionBound` read, but not the placement consequence this round added: with `ExcludePod` set after an unacknowledged reclaim, a retry never issues a fence RPC to a pod that may still hold the prior attempt's entry, so that refusal class is narrowed further. My grant on summary.md covers the deliverable index and statements my own edits falsify, and this row is neither, so the addition is left for the pass between the loops.

USEFUL [G1, G2 hand-off notes]: both reported exactly which sentences they had already rewritten inside the SPEC-2 block, so the block was relocated and re-scoped as a unit with their edits intact (G2's closing "Neither the reclaim nor the refused start reports a cleanup outcome" sentence and G1's §5.2 anchor split both survive verbatim), and the "Spec files touched" spec/05 entry was appended to rather than rewritten, which is what preserved SPEC-3's two anchors.

### [spec.1.fix-design-G1.1]

DECISION: The runtime teardown's precondition is restated as "a session whose `StartSession` the adapter has admitted" (the slot claim inside `StartSession`), and the phrase "the pod's shared runtime process has been given" is retired from this proposal entirely — BECAUSE that phrase is already bound, in four spec sections and two docs pages, to the runtime-cohort / exactly-one-session rule (spec/04_system-components.md:962, spec/15_external-api-surface.md:1734, spec/28_communication-channels.md:1194,1242,1735, spec/29_communication-scenarios.md:308,1521, docs/runtime-author-guide/integration-levels.md:99, platform-tools.md:23), and the code's own definition of it is `noteRuntimeStarted`/`runtimeLive` (pkg/adapter/runtimegeneration.go:21-22,70-73), which is set AFTER `Runtime.Start` returns. The staged code gates on `st.started`, set before `Runtime.Start`. ALTERNATIVES: (a) move the code to `runtimeLive` to match the sentence — rejected, it fails open in the start-in-flight window, leaving the shared runtime serving an abandoned session; (b) gloss "has been given" to mean the admitted start — rejected as hair, it redefines a phrase four other sections bind differently; (c) move §6.2's `running` boundary to cohort membership and edit the fence annotation plus docs/reference/state-machines.md:235 — rejected, it enlarges the edit into pre-existing text and puts a slot whose runtime is genuinely running it into `receiving_uploads`; (d) a new sub-state between `receiving_uploads` and `running` — rejected, new state with no observer.

FACT: the three adapter predicates are strictly nested and are three different fields — entry present (`s.slots[id]`) ⊃ bound (`st.sessionID != ""`, set by BOTH `assignCredentialsSlot` and `claimSessionSlotUnderLock`) ⊃ started (`st.started`, set only in `claimSessionSlotUnderLock`) ⊃ runtimeLive (set only by `noteRuntimeStarted`). EVIDENCE: pkg/adapter/slotcreds.go:32-34; pkg/adapter/slotsession.go:87-88; pkg/adapter/runtimegeneration.go:35-48; pkg/adapter/session.go:111,156,163.

FACT: §6.2's per-slot `receiving_uploads → running` edge is annotated "workspace ready, session dispatched to runtime with its session identifier", which names the StartSession dispatch, and `ValidTransitions()` already carries `{Running, SlotCleanup}` and `{Running, Failed}`. So the third residue class (start admitted, gateway abandoned) is a slot already in `running` and needs no new edge; the new `receiving_uploads → slot_cleanup` edge covers the first two classes only. The proposal's Design sentence "One edge ... covers all three residue classes" is true only under the rejected cohort reading. EVIDENCE: spec/06_warm-pod-model.md:152; pkg/sandbox/slotstate/slotstate.go:105-112.

FACT: nothing in production drives `slotstate.Registry.Transition` through `ReceivingUploads → Running`; only `MarkLeaked`/`MarkReleased` are used. The per-slot sub-state machine is a contract model, so no shipped code disagrees with either reading of the `running` boundary, and CODE-3 is the only code touched by it. EVIDENCE: pkg/sandbox/slotstate/registry.go:12-20; grep for slotstate.Running outside the package returns no production caller.

FACT: §5.2's slot-cleanup action list is exactly three actions — workspace directory, process group, `slotId` — and states neither the per-slot credential directory nor any §4.9 timer disarm, while §5.2's own recycle-lifecycle prose 90 lines earlier already presupposes the wider list ("after every ended session's per-slot tree and credential lease have been removed"). The code does both on every slot cleanup. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545 and :455; pkg/adapter/slotlayout/tree.go:58-68 (RemoveTree sweeps `p.CredentialsDir`); pkg/adapter/slotsession.go:174-186 (deregisterSlotLocked cancels every armed timer).

DECISION: close finding 2 by widening §5.2's action-list sentence under SPEC-3 rather than enumerating the actions in the §4.7 `Shutdown` row — BECAUSE the delegation in the row is then true, one canonical list survives, and §5.2 stops being incomplete for the ordinary session end as well as for the new pre-start reclaim. ALTERNATIVES: enumerate in the §4.7 row and drop the delegation (two statements of one list, §5.2 still wrong one section over); weaken the Design paragraph to claim only what §5.2 states (leaves two of the four artifacts Goal 1 names with no stated obligation).

WATCHOUT: the tempting local fix on finding 4 is to change `pkg/adapter/session.go` to gate on `runtimeLive` so the code matches the staged sentence. It inverts the fail-closed direction: CODE-2's rollback only fires when `Runtime.Start` returns, so a start that hangs would leave the runtime serving the abandoned session forever. EVIDENCE: pkg/adapter/session.go:155-163.

WATCHOUT: do not edit spec/06_warm-pod-model.md:152 or docs/reference/state-machines.md:235. They already state the admitted-start boundary in the words "dispatched to the runtime with its session identifier"; the corrected SPEC-4 prose anchors to that annotation instead of replacing it, which is what keeps DOCS-1 to the single added row it stages.

UNVERIFIED: nobody has checked whether tests/tier7a_load_local/shutdown_drain_gate_race_test.go asserts the drain gate against the binding or against `started`. The CODE-1 split moves the drain inside the `started` block (still additionally gated on `!boundRemains`), so a test asserting a drain for a bound-but-unstarted entry would flip. The S7 implementer should read it before landing.

OPEN: neither finding in G1 changes `boundSlotState`'s predicate (`st.sessionID != ""`), so proposal 0080 §1.19's three fence-refusal classes are untouched by this group. What does move the membership is CODE-4's compensation timing, which turns a reclaimed session from "bound" into "absent" — both already answer the same FailedPrecondition at pkg/adapter/coordination.go's fence call, which is exactly 0080 §1.19's complaint. A later group should state that in the proposal rather than leaving 0080 to discover it.

### [spec.1.fix-design-G2.1]

DECISION: One reporting rule, anchored on the §6.2 `running` boundary, stated once in spec/05 §5.2's `**Scrub model.**` paragraph (spec/05_runtime-registry-and-pool-model.md:453): a per-slot cleanup on a slot that never reached `running` reports no outcome, and a session release produces at most one cleanup-outcome report, filed by the cleanup that reclaimed the slot. SPEC-3 moves there from the `**Slot cleanup:**` bullet; SPEC-2's closing sentence and the CODE-2 rollback stop reporting; the code gate for `reportSessionScrub` moves from `st.started` to `runtimeLive` membership. BECAUSE `runtimeLive` is the tree's own statement of "sessions given to the pod's one shared runtime process" (pkg/adapter/runtimegeneration.go:1-19,36-49), which is verbatim what §6.2 says `running` means, so one predicate serves the spec sentence and the code, and every interleaving then yields exactly zero or one report. ALTERNATIVES: (a) the reviewer's preferred "reclaim reports, refused start does not" — rejected, it keeps one report but files it for a session the pod never ran, which is the very miscount SPEC-3 exists to prevent and re-opens the SPEC-3 contradiction at the racing boundary; (b) keep both reports and stage a §4.7 `ReportSessionScrub` row exemption so a `leaked` report skips `sessionsServed` — rejected as hair (an exception clause on a shipped RPC row, and it would need the proto/gateway handler, which S-2 forbids); (c) widen §6.2's fence so `slot_cleanup ──→ leaked` sits in the either-concurrency block — rejected, it changes shipped state-machine semantics, breaks docs/reference/state-machines.md:251 and its tier-11 pair test, and is not this proposal's subject.

FACT: `st.started` is set inside `claimSessionSlotUnderLock` BEFORE `Runtime.Start` (pkg/adapter/slotsession.go:86-87, called from pkg/adapter/session.go before :156), so it means "a start was claimed", never "the runtime was given the session". `runtimeLive` is set by `noteRuntimeStarted` after `Runtime.Start` returns and cleared by `noteRuntimeClosed`. The proposal's CODE-1 correctly wants the over-approximating `st.started` for the runtime TEARDOWN (fail closed) and wrongly reuses it for the REPORT (must fail closed the other way). EVIDENCE: pkg/adapter/runtimegeneration.go:21-68; pkg/adapter/slotsession.go:63-89.

FACT: `RecordSessionScrub` discards the session identifier and calls `IncrementSessionsServed` unconditionally before it branches on `leaked`, then drives the per-release `maxSessionsPerPod` retirement off that post-increment count. There is no per-session dedup anywhere on that path, so two reports for one session are two served sessions and one bogus leak-ledger entry. EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481.

WATCHOUT: the leak ledger is NOT concurrency-gated. `UnhealthyThreshold(1) == 1`, so a single `leaked` report on a `maxConcurrentSessions: 1` pod stamps `lenny.dev/drain-request` immediately. EVIDENCE: pkg/gateway/session/recycle/scrubreporter_seams.go:184-197; pkg/gateway/runtime/slothealth/slothealth.go:121-140. A `leaked` report used as a "retire this pod" side-channel therefore retires the pod on the spot, which is why the earlier draft reached for it.

MISTAKE: the draft used a `leaked` `ReportSessionScrub` from the racing `StartSession` rollback as the carrier for "this pod is bricked, retire it" (non-spec-changes.md:189-195, :584-588). That is an accounting RPC used as a health side-channel, and it cost the proposal a self-contradiction with SPEC-3 plus a double `sessions_served` advance.

FACT: the "bricked pod" premise is real but not distinctive to the racing rollback. `SocketRuntimeProcess.Close` closes the listener whenever the released session was the last active one (pkg/adapter/socketruntime.go:441-467), and the listener is bound exactly once, at adapter start (pkg/adapter/socketruntime.go:151-161, cmd/lenny-adapter/main.go:354). An ORDINARY session end reaches the same state through the same call, so retiring only on the racing path fixes no class. This is why deleting the report is safe: nothing distinguishes the racing case from a normal release here.

OPEN: does a recycling pool with a socket runtime work at all? After any last `Runtime.Close` the listener is gone, so the next session's `Runtime.Start` (pkg/adapter/socketruntime.go:181-221) spawns and then accepts on a closed listener. Either socket runtimes never land on recycling pools or the recycle path is broken for them. Pre-existing; not caused or repaired by 0081. Worth filing as its own finding against pkg/adapter/socketruntime.go.

SEPARATE FINDING (do not fix here): §6.2's fence scopes `slot_cleanup ──→ leaked` to "Per-slot sub-states scoped to concurrent occupancy" (spec/06_warm-pod-model.md:146-148), while §6.2's own `**`leaked` slot semantics**` paragraph, §5.2's `ceil(maxConcurrentSessions/2)` threshold at 1, and `slothealth.Tracker` all apply `leaked` at concurrency 1. The fence and the prose disagree, pre-existing. 0081 sidesteps it by deleting the terminal claim from SPEC-4's prose rather than widening the fence.

WATCHOUT: the `**Slot cleanup:**` bullet at spec/05:545 sits under the heading `**Slot failure and cleanup (`maxConcurrentSessions > 1`).**` at spec/05:542. Anything landed in that bullet does not exist for a single-session pod. The concurrency-independent home for a per-slot scrub rule is the `**Scrub model.**` paragraph at spec/05:453 ("uniform across session-mode configurations").

FACT: this group does not move the bound/unbound membership that 0080 §1.19's `boundSlotState`/`checkSessionBound` read (pkg/adapter/slotsession.go:274-290): those read `st.sessionID`, and G2 changes only which predicate gates `reportSessionScrub`. The membership change 0080 §1.19 must account for comes from CODE-1's `bound` → `removed`/`started` split, not from G2.

UNVERIFIED: whether the existing tier-1 fixture for the CODE-1 "Bound and started" case drives `noteRuntimeStarted` (i.e. puts the session in `runtimeLive`). Under the new gate the cleanup-outcome assertion in that case depends on it. Whoever implements CODE-1 must check the fixture rather than assume. EVIDENCE: non-spec-changes.md:445-447.

### [spec.1.fix-design-G3.1]

DECISION: Replace SPEC-2's no-retry clause with a PLACEMENT rule, not a retryability rule — BECAUSE "the failed attempt is not retried" is a claim owned by §5.2's closed three-item non-retryable list, §6.2's two-item list, §15.1's retryable fallback and three error-catalog rows, and staging the exception into all of them is ~11 mirrored exception clauses plus a new `SlotReason` value (§5.2's "Client error on exhaustion" sets `error.category` from the failure reason, and an unacknowledged reclaim is a disposition, not a reason). The true constraint is narrower: the retry must not land back on THAT pod. Stated as placement it contradicts nothing and every one of those sites stays true verbatim. ALTERNATIVES: (a) widen the exception into §5.2/§6.2/§15.1 + docs/reference/error-catalog.md — rejected, ~11 sites, new reason value, and it withdraws the retry exactly during the partial pod outage that made the reclaim fail; (b) delete the no-retry clause and leave the retry unconstrained — rejected, the hazard is real (see WATCHOUT below); (c) fence the lagging Shutdown by a per-attempt generation — BLOCKED, needs a `ShutdownRequest` field and programme rule S-2 reserves the single `schemas/lenny-adapter.proto` edit to step R1b.

DECISION: Lift the SPEC-2 block out of §7.1's "Atomicity of session creation (steps 2–8)." paragraph into its own paragraph immediately after it, re-scoped from "a finalize-block or start failure" to any gateway bind attempt including the §7.3 re-attach — BECAUSE the host paragraph is creation-scoped by its heading and closes "The client never receives a `session_id` for a session that failed to fully initialize", which is false of a resumed session whose row is already persisted, and because a rule about retries reads as a client-retry rule when inserted two sentences after "returns a retryable error to the client: `503 SERVICE_UNAVAILABLE` ... `Retry-After`". ALTERNATIVES: widen the trigger in place (rejected: wrong host paragraph); drop the resume compensation from CODE-4/S9 (rejected: the residue on resume is real and it is the recovery path).

FACT: `SlotID == SessionID` on every path and the per-slot tree is `/workspace/slots/{sessionId}/`, so a §5.2 retry of the SAME session is NOT a fresh slot key when it lands on the same pod — it is the same adapter registry key and the same on-disk tree. §5.2's "Fresh workspace guarantee" ("a retried slot always receives a fresh workspace ... even if the failed slot's cleanup has not yet completed") silently assumes a distinct slot and does not hold here. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:29-31; pkg/adapter/slot.go:105-126 (ensureSlotStateLocked keys on the session id and creates the tree); spec/05_runtime-registry-and-pool-model.md:556

WATCHOUT: do not "simplify" this by just deleting the no-retry clause. With the retry unconstrained and a lagging `Shutdown` still executing under the same identifier, two unfenced outcomes exist: (1) the reclaim lands between the retry's PrepareWorkspace and its StartSession — StartSession's `claimSessionSlot` re-creates an EMPTY entry and tree and starts the session on it, silently; CODE-2's survived-the-start confirm does NOT catch this, because the entry it checks is the one its own claim just recreated. (2) the reclaim lands after the retry is running and tears down a healthy session. EVIDENCE: pkg/adapter/slotsession.go:74-90 (claimSessionSlotUnderLock → ensureSlotStateLocked recreates on miss)

FACT: `ShutdownRequest` already carries `coordination_generation` (schemas/lenny-adapter.proto:1630-1634) and the adapter validates it, but the generation is per-session-coordinator and does not advance between two attempts of the same session on the same replica, so it cannot fence attempt N's lagging Shutdown from attempt N+1's entry. The fence looks like a free answer and is not.

FACT: the hazard is only reachable at `maxConcurrentSessions >= 3`. `UnhealthyThreshold` is `(maxConcurrent+1)/2`, so at 2 one leak already drains the pod inside `applySlotRetryPolicy` before the retry iteration. EVIDENCE: pkg/gateway/sessionserver/start.go:2856-2874; the proposal's own non-spec-changes.md:389-395 states the same arithmetic.

FACT: `Binder.Resume` does NOT run through `applySlotRetryPolicy`. `resumeOnPod` calls `b.Resume` directly, so the finding-0 retry rule and the finding-1 resume compensation are disjoint code paths; the resume retry is §6.2's `resuming` transitions plus §7.3's client-facing retryPolicy. EVIDENCE: pkg/gateway/sessionserver/start.go:3494,3943; pkg/gateway/podlifecycle/podsession/binder.go:1590-1630

MISTAKE: the proposal already saw the placement-exclusion option and rejected it as "the larger alternative ... not staged here" (non-spec-changes.md:574-579), pricing only its code cost and never pricing the spec-contradiction surface the no-retry rule creates. Priced honestly the exclusion is 4 small Go edits and 2 spec clauses against ~11 spec/doc sites and a new reason value. Do not re-derive that bullet's conclusion; it is the finding.

FACT: `expiredByUptime` is the existing precedent for a read-only slot-placement skip predicate, and `ClaimSlot` has exactly two candidate loops (pass 1 same-tenant claimed pods, pass 2 fresh idle pods) each already using `continue`-based skips. The exclusion is one `continue` per pass. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:336-357, :416-478

UNVERIFIED: whether a §6.2 `resuming` retry (resume_pending → resuming again) can re-pick the pod that holds an unacknowledged reclaim. `Binder.Resume` claims through `b.connect`, which does not take the exclusion field this design adds to the slot-claim path. Someone should check `connect`'s pod selection before assuming the resume path is covered.

OPEN: with the exclusion in place, when the excluded pod is the only candidate the retry surfaces as `WARM_POOL_EXHAUSTED` / `concurrent_slots_exhausted`, which is honest (retryable 503) but names the wrong cause. Deliberately NOT minting a new `details.reason` value — that would be hair. A later round may decide otherwise.

DEFERRED [docs/reference/error-catalog.md]: nothing. Under this design lines 129, 155 and 156 stay TRUE, because no failure class becomes non-retryable. Recorded explicitly so a later round does not re-file them: they are sites only under the rejected alternative.

### [spec.1.review-applicability.1]

FACT: Every anchor the spec staging quotes is verbatim-correct in the tree today. Verified one by one:
§4.1 third sentence at spec/04_system-components.md:157; the `Shutdown` row opening at
spec/04_system-components.md:686; §4.7.9 step 5 at spec/04_system-components.md:853; the §7.1
parenthetical and the "...pre-attached disposition." / "The client never receives a `session_id`"
insertion seam at spec/07_session-lifecycle.md:23; the `ReportSessionScrub` sentence at
spec/05_runtime-registry-and-pool-model.md:545; the §6.2 fence heading line at
spec/06_warm-pod-model.md:150, the `receiving_uploads ──→ running` entry at :152-153, the fence
close at :156 and the `**`reserved` hold semantics.**` paragraph at :158. Every link anchor the
staged text mints resolves (`#479-startup-sequence-for-type-agent-runtimes` is used today at
spec/README.md:36; `#1542-rpc-lifecycle-state-machine` at spec/15_external-api-surface.md:2017).
Do not re-do this sweep; spend the effort on semantics instead. EVIDENCE: spec/04_system-components.md:157,686,853

FACT: §7.1's atomicity paragraph lives INSIDE the fenced code block that spans
spec/07_session-lifecycle.md:5-54. The staged SPEC-2 block therefore lands inside a ``` fence, as
the existing markdown links in that paragraph already do. This is pre-existing and is not a defect
of this proposal. EVIDENCE: spec/07_session-lifecycle.md:5,23,54

FACT: `Shutdown` is one of the few adapter handlers with NO `checkSessionBound` guard
(pkg/adapter/session.go:227 vs. attach.go:41, lifecycle.go:30, usage.go:266), so SPEC-1's new
"a session the adapter holds no entry for ... answers with a clean-exit response" sentence is
reachable today and needs no fence carve-out. EVIDENCE: pkg/adapter/session.go:227-241

FACT: The gateway, not only the adapter, already writes the `lenny_adapter_leaked_slots` gauge and
already marks a slot leaked without the adapter knowing (`applySlotRetryPolicy`'s
`slots.MarkLeaked` on a failed `ReleaseSlotReservation`). So the proposal's gateway-side `leaked`
disposition introduces no new asymmetry with §6.2's "equivalently the leaked portion of the pod's
Redis slot-counter occupancy". I chased this and it is a dead end.
EVIDENCE: pkg/gateway/sessionserver/start.go:2836-2848, pkg/gateway/sessionserver/sessionserver.go:417-421

FACT: `ReportSessionScrub` really does advance `sessions_served` on the gateway, with no
per-session dedup: `RecordSessionScrub` calls `IncrementSessionsServed` unconditionally and then
`RetireOnSessionCount`. SPEC-3's stated rationale is therefore true of the tree.
EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-480, pkg/agentpodstate/memstore/memstore.go:205-222

DEFERRED [non-spec-changes / docs]: `docs/reference/adapter-contract.md:75` carries the
reader-facing `Shutdown` row and states the behaviour SPEC-1 and SPEC-3 change: "The adapter
flushes the session's final usage report, closes its runtime, removes its slot tree, and reports
the per-slot cleanup outcome through `ReportSessionScrub`." After the edits that is wrong in three
ways (the runtime teardown becomes conditional on the session having reached the runtime, the
report is withheld for a pre-start reclaim, and the no-op answer for an unheld session is
unstated). The only DOCS deliverable staged is DOCS-1 for `docs/reference/state-machines.md`
(non-spec-changes.md:405,621); `adapter-contract.md` appears in no edit list. The non-spec loop
must add it. EVIDENCE: docs/reference/adapter-contract.md:75

DEFERRED [non-spec-changes CODE-2]: the staged race rollback sends a SECOND
`ReportSessionScrub` for the same session. In the race the compensating `Shutdown` sees
`st.started == true` (set inside `claimSessionSlotUnderLock` before `Runtime.Start`), so CODE-1's
`if started { s.reportSessionScrub(...) }` fires (non-spec-changes.md:107-109), and then
`StartSession`'s rollback fires `s.reportSessionScrub(ctx, sessionID, errStartRaceReclaimed)`
(non-spec-changes.md:195). `RecordSessionScrub` has no per-session dedup, so `sessions_served`
advances twice for one abandoned session — the exact double-count SPEC-3's rationale says must be
avoided. I did not file it because the remedy is in code, not in the staged spec text.
EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-465

WATCHOUT: `On the default disposition the pod is replaced.` (spec/04_system-components.md:686,
left unchanged by SPEC-1) looks like it contradicts the staged §7.1 sentence "this reclaim governs
the slot state on a pod that is released or reused rather than terminated". It is NOT a finding:
that sentence is already inaccurate today for a concurrent pod whose co-tenant ends at nonzero
occupancy, so the proposal does not introduce it. Do not spend a verifier on it.
EVIDENCE: spec/04_system-components.md:686, spec/06_warm-pod-model.md:132-134

WATCHOUT: `slot_cleanup ──→ leaked` sits under the fence's "Per-slot sub-states scoped to
concurrent occupancy" heading while SPEC-4 adds its new edge to the "either concurrency" list and
its prose asserts a "`released`-or-`leaked` terminal". The mismatch is pre-existing (the same
applies to today's `running ──→ slot_cleanup`), the code's `slotstate` edge list ignores the
concurrency scoping entirely, and §5.2 / §6.2's leaked-semantics prose state the leaked outcome
unconditionally. I judged it below the bar. EVIDENCE: spec/06_warm-pod-model.md:146-155, docs/reference/state-machines.md:251

UNVERIFIED: whether the compensating `Shutdown`'s `coordination_generation` (left at the client
default by `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", 0)`) is validated anywhere on the
adapter's `Shutdown` path. I found no generation check in `pkg/adapter/session.go`'s handler, only
in `CoordinatorFence` / `CheckpointBarrier` (pkg/adapter/coordination.go:120,262). Someone on the
code loop should confirm the fence does not refuse the compensation.

### [spec.1.review-citations.1]

FACT: Every verbatim "text to replace" block in the spec staging matches the tree exactly and each anchor is unique. Machine-checked all five (§4.1 sentence, §4.7 `Shutdown` row opening, §7.1 parenthetical, §4.7.9 step 5, §5.2 `ReportSessionScrub` sentence) plus the three inline anchor phrases. Do not re-verify by hand; re-run the substring check if the spec moves. — EVIDENCE: spec/04_system-components.md:157, spec/04_system-components.md:686, spec/04_system-components.md:854, spec/05_runtime-registry-and-pool-model.md:545, spec/07_session-lifecycle.md:23

FACT: All six markdown anchors the staged text emits resolve (`#47-runtime-adapter`, `#479-startup-sequence-for-type-agent-runtimes`, `#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`, `#1542-rpc-lifecycle-state-machine`). — EVIDENCE: spec/04_system-components.md:657,848; spec/05_runtime-registry-and-pool-model.md:365; spec/06_warm-pod-model.md:78; spec/07_session-lifecycle.md:3; spec/15_external-api-surface.md:1686

FACT: The shipped `Shutdown` handler gates the WHOLE teardown (final usage, drain signal, `Runtime.Close`, `removeSlotTree`, `reportSessionScrub`) on `bound := removed && st.sessionID != ""`, and gates only the §15.4.2 drain signal additionally on `!boundRemains`. `boundRemains` is "some surviving entry has a non-empty sessionID". The proposal's "the signal ... goes out only when the deregistration leaves the adapter holding no bound entry" is a faithful restatement of the shipped code, near-verbatim from its comment. — EVIDENCE: pkg/adapter/session.go:238-241,259; pkg/adapter/slotsession.go:174-188

FACT: `AssignCredentials` is what binds (`st.sessionID = sessionID` when empty), preceding `StartSession`; `claimSessionSlotUnderLock` sets both `sessionID` and `started`. So "bound" and "started" are genuinely two different registry predicates today. — EVIDENCE: pkg/adapter/slotcreds.go:30-37; pkg/adapter/slotsession.go:85-88

FACT: `stageWorkspace` sends `PrepareWorkspace` only under `if len(uploads) > 0`, so an upload-free plan reaches `FinalizeWorkspace` as the first adapter RPC. The proposal's edge-case bullet on this is accurate. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1323-1330

FACT: `ShutdownRequest` carries `coordination_generation`, but the adapter validates that field only in `CoordinatorFence` and `CheckpointBarrier` — `Shutdown` is unfenced today, so the staged compensation RPC is not blocked by the §10.1 fence. — EVIDENCE: schemas/lenny-adapter.proto:1635; pkg/adapter/coordination.go:120,262

WATCHOUT: §5.2's `slot_cleanup ──→ leaked` edge sits in the "Per-slot sub-states scoped to concurrent occupancy" block of §6.2's fence (line 148) while `slot_cleanup ──→ released` and the new `receiving_uploads ──→ slot_cleanup` edge sit in the "either concurrency" block (lines 150-156). I considered filing that the new edge's `leaked` terminal is unreachable on a single-session pod, and did NOT file it: the same asymmetry already exists for the shipped `running ──→ slot_cleanup` edge, so it is pre-existing drift rather than something this proposal introduces. — EVIDENCE: spec/06_warm-pod-model.md:146-156

MISTAKE: I nearly filed that the staged §7.1 "not retried" clause bans retries for every post-first-RPC bind failure. It does not: the clause is grammatically governed by "A reclaim the adapter does not acknowledge", so it bans the retry only for the unacknowledged-reclaim class. The non-spec staging confirms that reading (`applySlotRetryPolicy` skips the retry only when `sbe.Leaked`). Read the sentence's subject before filing on it. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:148; proposals/0081_.../0081_....non-spec-changes.md:376-380

UNVERIFIED: SPEC-3 makes the pre-start reclaim report no `ReportSessionScrub` outcome, and §4.7's `ReportSessionScrub` row (spec/04_system-components.md:692) says `leaked` outcomes feed the unhealthy-threshold ledger. Nobody has checked whether an adapter-internal cleanup failure on a pre-start reclaim (an acknowledged `Shutdown` whose `removeSlotTree` failed) can then reach the §5.2 threshold at all. The non-spec staging routes it through `exited_cleanly` instead; a later reviewer should confirm the spec says enough for that.

### [spec.1.review-client-surface.1]

FACT: no per-slot sub-state is client-visible, so SPEC-4's new edge has exactly two parallel
representations outside `spec/06`: the `docs/reference/state-machines.md` per-slot table
(DOCS-1 covers it) and `pkg/sandbox/slotstate` (CODE-3). `pkg/api/v1/session` deliberately
excludes them and §15.1 says they are never returned externally. Do not go hunting for an
OpenAPI, SDK, or CRD mirror of the slot machine; there is none. — EVIDENCE:
pkg/api/v1/session/session.go:9-13; spec/15_external-api-surface.md:672;
docs/reference/state-machines.md:233-237

FACT: `ShutdownResponse` is `{exited_cleanly, exit_code}` and the shipped handler already
answers `ExitedCleanly: closeErr == nil` for a session it holds nothing for, so SPEC-1's
"clean-exit response" needs no proto field and no SDK change. — EVIDENCE:
schemas/lenny-adapter.proto:1665-1668; pkg/adapter/session.go:227-289

FACT: the §10.1 fence does NOT contradict SPEC-1's no-op sentence. §10.1's rule rejects a
*stale* generation, and §10.1's own restatement scopes it to "a session bound to the pod ...
when it holds a generation for that session"; an adapter holding no entry holds no generation
and accepts. `Shutdown` also does not call the fence today. — EVIDENCE:
spec/10_gateway-internals.md:30,:183; pkg/adapter/session.go:227-240

FACT: `RecordSessionScrub` increments `sessions_served` BEFORE the leaked branch, so a
`leaked` report advances `recycle.maxSessionsPerPod` retirement exactly as a `released` one
does. Any staged sentence that has the adapter report an outcome for a session the pod did not
serve is making that advance. — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:450-462

DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC comment ("asks the adapter to
terminate the agent and release the pod ... Returns when the agent process has exited") and the
`ReportSessionScrub` / `SessionScrubOutcome` comments ("on every session release") are the
proto-side statement of the contract §15.4 says is "kept in sync with the prose". SPEC-1's
two-teardown split, its no-op clean-exit answer, and SPEC-3's withheld report all falsify parts
of them. The programme's rule S-2 bars this proposal from opening that file, so the correction
belongs to the step that owns it. Note the Shutdown comment is ALREADY stale for a co-tenanted
pod, so this is a widening rather than a new break. — EVIDENCE:
schemas/lenny-adapter.proto:203-206,:308-311,:436-438;
spec/15_external-api-surface.md:1456

UNVERIFIED: SPEC-2's first edit inserts "and reclaims the state the attempt created on the pod"
into §7.1's *steps 2-8* rollback parenthetical, but §7.1's own numbered flow contacts no adapter
before step 10 (§29.2 steps 1-10 name no `adapter` participant either), and the same staged block
says "The obligation begins with the first such RPC". The parenthetical is therefore vacuous at
the stage range it is attached to. I judged this too weak to file; somebody deciding the final
wording should move it to the finalize-block sentence instead. — EVIDENCE:
spec/07_session-lifecycle.md:5-22; spec/29_communication-scenarios.md:195-201;
spec-changes.md:132-136,:148

OPEN: the compensating `Shutdown` on a bound-but-unstarted exclusive-pod slot fires the §15.4.2
`terminate` frame, whose `reason` is a closed four-value enum (`session_complete`,
`budget_exhausted`, `eviction`, `operator`) and none of them names an abandoned bind;
`drainReason` maps anything unrecognized to `session_complete`. A Full-level runtime therefore
sees `session_complete` for a session that never ran. The wire stays valid, so I did not file it,
but nobody has decided whether that is the intended reason value. — EVIDENCE:
schemas/runtime-ops-events.schema.json:174-185; pkg/adapter/session.go:316-325

USEFUL [spec.1.review-edit-sites.1]: its anchor-resolution FACT saved me re-deriving all six
markdown anchors, and its `sessions_served` FACT pointed straight at the scrub-report line that
turned my first finding from a wording quibble into an operator-visible consequence.

### [spec.1.review-docs-alignment.1]

FACT: `st.started` is set at `pkg/adapter/slotsession.go:88` inside `claimSessionSlot`, which
`StartSession` calls at `pkg/adapter/session.go:111`, while `s.Runtime.Start` runs at
`pkg/adapter/session.go:156` and `noteRuntimeStarted` at `:163`. The gap is not a hairline race:
it spans `sessionConnectors`, `writeSessionManifest`, `startPlatformMCP`, and
`startConnectorMCPServers`. Any spec sentence that equates `started` with "the runtime has been
given the session" is false across that whole span. — EVIDENCE: pkg/adapter/session.go:111-163

FACT: `lenny_adapter_leaked_slots` is emitted by the GATEWAY
(`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223`), not by the adapter,
despite the name and despite §6.2 calling it the adapter's count. So withholding the adapter's
`ReportSessionScrub` on a pre-start reclaim does NOT blind the gauge: CODE-5's
`accountSlotFailure` feeds it from the gateway side. I checked this before filing a
metric-divergence finding and it refuted itself. — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219

FACT: `exited_cleanly` has no definition anywhere in `spec/` or `docs/`; it exists only at
`schemas/lenny-adapter.proto:1666`. The proposal's "`exited_cleanly` carries two meanings" edge
case therefore falsifies no spec or doc sentence, and needs no spec edit. — EVIDENCE: schemas/lenny-adapter.proto:1666

DEFERRED [0081 non-spec-changes.md, `## Staged docs changes`]: DOCS-1 stages only
`docs/reference/state-machines.md`. `docs/reference/adapter-contract.md:75` states the `Shutdown`
row as ONE teardown — "The adapter flushes the session's final usage report, closes its runtime,
removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`" —
which is the pre-change §4.7 contract. After SPEC-1 and SPEC-3 that sentence is false in three
ways: the slot release now runs for an unbound entry, the runtime teardown and the usage flush
run only for a started session, and a pre-start reclaim reports no outcome. The row also needs
the no-op answer for a session the adapter holds no entry for. Secondary, same cause:
`docs/reference/adapter-contract.md:81` ("at each session release ... The gateway increments the
pod's served-session count"), `docs/operator-guide/security-principles.md:33`,
`docs/reference/execution-modes.md:68`, and `docs/operator-guide/multi-tenancy.md:72` each say the
per-slot cleanup runs "at each session release" and reports its outcome, which SPEC-3 widens
(cleanup also runs on an abandoned bind) and narrows (that one reports nothing). The next loop
owns these; they are not spec defects.

UNVERIFIED: SPEC-1 leaves the rest of the §4.7 `Shutdown` row unchanged, including "On the
default disposition the pod is replaced." (`spec/04_system-components.md:686`). The compensating
reclaim sends `Shutdown` on the default disposition for a pod that must keep running (it may hold
a live co-tenant), which reads against SPEC-2's "this reclaim governs the slot state on a pod that
is released or reused rather than terminated". I did not file it because the sentence is already
loose today for any per-slot `Shutdown` on a co-tenanted pod, so a verifier can call it
pre-existing. Someone should decide whether the row needs "on the default disposition at the
session's release". — EVIDENCE: spec/04_system-components.md:686

UNVERIFIED: with the `started` predicate as staged, a `Shutdown` arriving between
`session.go:111` and `:156` on a pod with an empty runtime active set takes
`SocketRuntimeProcess.Close`'s last-close teardown (shared conn, spawned child, never-rebound
listener) — the exact brick the proposal's "Watch out for" says the gate prevents — and the
gateway sees a clean close, so `Leaked` is false and the bricked pod returns to inventory. This is
a mechanism/feasibility question, not a docs one; whoever owns CODE-1/CODE-2 should settle it.
— EVIDENCE: pkg/adapter/socketruntime.go:441-467, proposals/.../summary.md:85-93

OPEN: §29's session-creation trace (`spec/29_communication-scenarios.md:205-290`) enumerates the
bind RPCs and states a failure branch only for `ConfigureWorkspace` (step 23). SPEC-2 gives step-5
a failure branch in §4.7.9 but adds nothing to §29. §29's own preamble says a trace restates and
the cited section is normative, so I judged it optional rather than a missed edit site.
— EVIDENCE: spec/29_communication-scenarios.md:23-25

### [spec.1.review-edit-sites.1]

FACT: Every verbatim anchor SPEC-1..SPEC-4 quotes exists and matches, and every markdown anchor
they build (`#47-runtime-adapter`, `#1542-rpc-lifecycle-state-machine`,
`#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`,
`#479-startup-sequence-for-type-agent-runtimes`) resolves. — EVIDENCE: spec/04_system-components.md:157,
:686, :848; spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:150,:152,:157;
spec/07_session-lifecycle.md:23; spec/README.md:36. Do not re-derive these; spend the round elsewhere.

FACT: §6.2's per-slot fence is split into two blocks and the split is load-bearing.
`running ──→ failed` and `slot_cleanup ──→ leaked` sit under "Per-slot sub-states scoped to
concurrent occupancy"; the other four sit under "a pod of either concurrency". §5.2's
`**Slot cleanup:**` bullet likewise sits under a heading scoped to `maxConcurrentSessions > 1`.
`pkg/sandbox/slotstate` ignores the split and holds all six edges in one set. — EVIDENCE:
spec/06_warm-pod-model.md:146-155; spec/05_runtime-registry-and-pool-model.md:542-545;
pkg/sandbox/slotstate/slotstate_test.go:13-20.

FACT: `ReportSessionScrub` increments `sessions_served` unconditionally, on a `leaked` outcome
as well as a `released` one. Any spec sentence that has the adapter report an outcome for a
session the pod did not serve advances `recycle.maxSessionsPerPod` retirement. — EVIDENCE:
spec/04_system-components.md:692; pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-457.

FACT: §6.2 "Pre-attached failure retry policy" (spec/06_warm-pod-model.md:283-290) and §5.2
"Slot retry policy" (spec/05_runtime-registry-and-pool-model.md:553-563) each carry a closed
enumeration of non-retryable categories. Any proposal that makes a new bind-failure class
non-retryable has to edit both, and §5.2's "Client error on exhaustion" bullet as well, which
states the no-retry condition as a two-way disjunction.

WATCHOUT: `§15.4.2` is the repo's established citation for the CH-RUNTIMEOPS graceful-shutdown
(`terminate`) signal even though the frame's own card is §28.5.3 and §15.4.2 only names the
DRAINING state. Do not file that as a false citation; the tree already cites it that way. —
EVIDENCE: pkg/adapter/drain_test.go:12,:53; spec/28_communication-channels.md:1082;
spec/15_external-api-surface.md:1686-1700.

DEFERRED [non-spec-changes / docs]: `docs/reference/adapter-contract.md:75` (the `Shutdown` row)
states the teardown as one act — "The adapter flushes the session's final usage report, closes
its runtime, removes its slot tree, and reports the per-slot cleanup outcome through
`ReportSessionScrub`" — which SPEC-1 and SPEC-3 falsify on three counts (two teardowns with two
preconditions, the no-op clean-exit answer for an unheld session, and the withheld report on a
pre-start reclaim). `docs/reference/adapter-contract.md:81` likewise says the gateway "increments
the pod's served-session count" on every per-slot cleanup report. Neither row is in the non-spec
edit list, which names only `docs/reference/state-machines.md`. — EVIDENCE:
proposals/0081_.../0081_....non-spec-changes.md:621.

DEFERRED [non-spec-changes / docs]: `docs/reference/state-machines.md:251` says the
`slot_cleanup -> leaked` edge "appl[ies] only to a pod serving more than one concurrent session".
DOCS-1 adds only the new `receiving_uploads -> slot_cleanup` row at :234-237; the prose at :251
still scopes the terminal the new edge needs away from a single-session pod.

UNVERIFIED: whether `Binder.Resume`'s compensation needs its own §7.3 sentence or whether a
widened §7.1 obligation sentence can carry it. Somebody should decide before S9 is built; the
staged §7.1 block is worded "A finalize-block or start failure", which does not reach a resume
onto a replacement pod.

### [spec.1.review-feasibility.1]

FACT: every verbatim anchor SPEC-1..SPEC-4 quotes is exact in the tree at this commit. §4.1 sentence spec/04_system-components.md:157; §4.7 `Shutdown` row spec/04_system-components.md:686; §4.7.9 step 5 spec/04_system-components.md:853; §5.2 `ReportSessionScrub` sentence inside the Slot-cleanup bullet spec/05_runtime-registry-and-pool-model.md:545; §6.2 fence heading and `receiving_uploads ──→ running` spec/06_warm-pod-model.md:148-151; §7.1 parenthetical and the sentence the block follows spec/07_session-lifecycle.md:23. Every anchor link the staged text writes resolves (§4.7 "Runtime Adapter", §5.2 "Pool Configuration and Execution Modes", §15.4.2 "RPC Lifecycle State Machine", §7.1 "Normal Flow", §6.2 "Pod State Machine", §4.7.9). Do not re-derive these. — EVIDENCE: spec/04_system-components.md:157,686,853

FACT: the §6.2 per-slot fence is TWO blocks, not one. `running→failed` and `slot_cleanup→leaked` sit under "Per-slot sub-states scoped to concurrent occupancy" (spec/06_warm-pod-model.md:145-147); the other four sit under "a pod of either concurrency" (:148-153). Six edges total, which is why `slotstate.ValidTransitions()` has six (pkg/sandbox/slotstate/slotstate.go:105-114) and the checklist's "seven-edge set" is right. — EVIDENCE: spec/06_warm-pod-model.md:145-153

FACT: `SlotClaimer.ReleaseSlot(ctx, name, recycle, leaked)` already skips the counter decrement when `leaked` is true, so the staged "releases the slot reservation afterwards" and "the leaked disposition holds the slot's occupancy" are reconcilable rather than contradictory. I checked this before filing and dropped the finding. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:818,832-838

FACT: the gateway's `ReportSessionScrub` handler does no per-session dedup; every report increments `sessionsServed`. That is what makes the double-report finding real. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:76-95

DEFERRED [non-spec-changes / docs staging]: `docs/reference/adapter-contract.md:75` states the `Shutdown` row as ONE teardown — "the adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`". After SPEC-1 and CODE-1 that is false for an entry the runtime was never given (tree removed, no usage flush, no close, no report). The proposal's staged docs list carries only `docs/reference/state-machines.md` (DOCS-1). The docs staging needs this row too, and tier 11 reconciles it. — EVIDENCE: docs/reference/adapter-contract.md:75; proposal non-spec-changes.md:398-411

DEFERRED [non-spec-changes, CODE-2]: `noteRuntimeStarted`'s guard closes only the interleaving where the reclaim's `Shutdown` lands AFTER `claimSessionSlot`. In the other order the reclaim removes the bound-unstarted entry, and `claimSessionSlotUnderLock` then RE-CREATES it through `ensureSlotStateLocked`, so `noteRuntimeStarted` finds a valid entry, returns true, and the start is not rolled back — residue class three by a new route. The window is tight (only two nil checks run before the claim) but the removal is destructive, so the adapter has no record that a reclaim happened. Any stronger guard needs a tombstone or a generation, which is code work. — EVIDENCE: pkg/adapter/slotsession.go:64-90 (`ensureSlotStateLocked` inside the claim), pkg/adapter/session.go:111-163

UNVERIFIED: the staged §7.1 predicate for the leaked disposition is "a reclaim the adapter does not acknowledge", while CODE-4 classifies `err != nil || !cleanly` as leaked and CODE-1 deliberately makes `exited_cleanly` false for a not-started reclaim whose tree removal failed. Whether "does not acknowledge" reads as covering an unclean answer is a judgement call; I judged it too soft to file. A later reviewer or the fixer may want the §7.1 sentence to say "does not acknowledge, or answers that the release did not complete". — EVIDENCE: proposal spec-changes.md:148; proposal non-spec-changes.md:112-118,281-289

WATCHOUT: `§5.2`'s "Slot failure and cleanup" and "Slot retry policy" blocks are both headed `maxConcurrentSessions > 1`, while §6.2's per-slot sub-states are explicitly "a pod of either concurrency". I checked whether SPEC-1's "the slot release is the §5.2 slot cleanup" over-reaches at concurrency 1 and concluded it does not, because the shipped §4.7 `ReportSessionScrub` row already generalises the per-slot cleanup to every session release with no concurrency qualifier (spec/04_system-components.md:692). Do not file that as a finding. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:542,553; spec/04_system-components.md:692

### [spec.1.review-fresh.1]

FACT: The §4.7 `Shutdown` row's retained tail begins "On the default disposition the pod is replaced." (spec/04_system-components.md:686). The compensating reclaim this proposal introduces is a DEFAULT-disposition `Shutdown` (`Client.Shutdown` passes `recycle: nil`, pkg/gateway/runtime/adapterclient/client.go:807-809), so that retained sentence now governs the compensation and says the pod retires — the opposite of SPEC-2's "a pod that is released or reused rather than terminated". — EVIDENCE: spec/04_system-components.md:686; proposals/.../0081...spec-changes.md:116-117,148

FACT: The occupancy-zero recycle `Shutdown` is a SECOND RPC that reuses the just-released session's identifier, so it always names a session the adapter holds no entry for; the adapter's clause three runs the whole-pod scrub regardless of whether clause two removed anything. Any blanket "a request naming a session the adapter holds no entry for removes nothing" rule must except it. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:563-578 ("it carries the last-released slot's session id"); pkg/adapter/session.go:281-291

FACT: §5.2's `**Slot cleanup:**` action list is exactly "removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the `slotId`". It does NOT name the per-slot credential file or the §4.9 expiry timers, even though `slotlayout.RemoveTree` removes `CredentialsDir` and `deregisterSlotLocked` cancels the timers. §6.4's tree list (spec/06_warm-pod-model.md:386) is also `/workspace/slots/`, `/sessions/`, `/artifacts/` only. Do not assume §5.2 "owns" the credential file. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; pkg/adapter/slotlayout/tree.go:58-69

FACT: Retryability is owned by two sections, both enumerating their own non-retryable sets: §5.2 "Non-retryable failure categories" (spec/05:557, three reasons) and §6.2 "Pre-attached failure retry policy" (spec/06:283-288). A new no-retry rule stated only in §7.1 leaves both enumerations stale. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:555,557; spec/06_warm-pod-model.md:283,288

FACT: The gateway's `slotstate.Registry` is only driven through `MarkLeaked`/`MarkReleased`/`ForgetPod`; nothing calls `Transition`, so the §6.2 edge list in `pkg/sandbox/slotstate` is a pure transcription gate, not a live state machine. A missing edge cannot break a runtime path today. — EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2856; pkg/sandbox/slotstate/registry.go

FACT: `SlotClaimer.ReleaseSlot(leaked=true)` skips the Redis decrement entirely and returns no recycle signal, so "release the reservation" and "the leaked disposition holds occupancy" are opposite operations on the same counter. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-840

WATCHOUT: Every verbatim anchor quoted by the staged edits was confirmed byte-for-byte (spec/04:157, spec/04:686, spec/04:854, spec/05:545, spec/06:150-153, spec/07:23) and every markdown anchor resolves (`#47-runtime-adapter`, `#71-normal-flow`, `#62-pod-state-machine`, `#52-pool-configuration-and-execution-modes`, `#1542-rpc-lifecycle-state-machine`, `#479-startup-sequence-for-type-agent-runtimes` — the last is used by spec/README.md:36). Do not re-spend a round on anchor verification.

OPEN: SPEC-1's §4.1 replacement retires the terms "per-session teardown" while the retained second sentence still says "The per-slot teardown and the whole-pod teardown are the same operation on the same address". After the split there are three named operations and the retained sentence names two that the new sentence does not define. Whether that needs a wording fix is a judgement the fixer should make while addressing the "runs neither teardown" ambiguity.

### [spec.1.review-kubernetes.1]

FACT: `st.started` is set inside `claimSessionSlotUnderLock` BEFORE `Runtime.Start` runs, so it
is true during the manifest write, the pod-MCP arming, and the whole of `Runtime.Start`. The
set of sessions the shared runtime process "has been given" is a different, later set named
`runtimeLive`, populated by `noteRuntimeStarted` after `Start` returns. — EVIDENCE:
pkg/adapter/slotsession.go:85-87 (`st.sessionID = sessionID; st.started = true`),
pkg/adapter/session.go:111 (claim) vs :155 (`s.Runtime.Start`) vs :162 (`noteRuntimeStarted`),
pkg/adapter/runtimegeneration.go:21-22 and :70-73 (`runtimeLive` / `soleSession` = "has been given").

WATCHOUT: the staged §4.1 and §4.7 text spells the runtime-teardown precondition as "a session
the pod's shared runtime process has been given", which is the verbatim definition of
`runtimeLive`. The design deliberately rejects `runtimeLive` in favour of `st.started`. Anyone
touching that sentence must keep the fail-closed widening visible, or an implementor who follows
the spec literally reintroduces the third residue class. — EVIDENCE:
proposals/.../0081...spec-changes.md:120 vs .../0081...summary.md:56-59 and
.../0081...non-spec-changes.md:72-77 ("runtimeLive membership would not").

FACT: the gateway's leaked predicate on the shipped Shutdown path is `err != nil || !cleanly`,
so an ACKNOWLEDGED-but-unclean response already leaks the slot. The staged §7.1 sentence states
only the "does not acknowledge" half. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543.

FACT: `RecordSessionScrub` increments `sessions_served` unconditionally, before it looks at the
leaked flag. So any `ReportSessionScrub` — `released` or `leaked` — advances the pod's
served-session count, which is exactly what SPEC-3's rationale says must not happen for a
session that never ran. — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-468.

FACT: three shipped sections state that the failure classes this proposal makes non-retryable ARE
retried, and none is in the proposal's "Spec files touched" list: spec/06_warm-pod-model.md:283-288
(pre-attached retry, 2 retries, non-retryable list = upload validation + policy rejection),
spec/05_runtime-registry-and-pool-model.md:555-560 (slot retry; the "Fresh workspace guarantee"
bullet explicitly authorises retrying "even if the failed slot's cleanup has not yet completed",
which is the exact hazard §7.1 cites as the reason to refuse), and
spec/15_external-api-surface.md:1136 ("Any other setup-window failure ... is recovered with a
fresh pod per Section 6.2"). spec/15 is untouched by the staging.

FACT (checked, NOT a finding): the SandboxClaim left `bound` by a leaked reclaim is not an
undeletable object. §4.6.1 orphan-GC predicate 1 drains a `bound`/`recycling` claim older than
`claimOrphanTimeout` whose pod no active session references, so the level-triggered backstop
exists. `SlotClaimer.ReleaseSlot(leaked=true)` returns before the counter decrement and before the
claim DELETE, by design. — EVIDENCE: spec/04_system-components.md:517,
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:836-843.

FACT (checked, NOT a finding): the gateway owns `SandboxClaim.spec` and `.status` outright, and
routes the unhealthy-threshold drain through the `lenny.dev/drain-request` annotation rather than
writing `Sandbox.status`. Nothing in this staging asks a component to write another's status
subresource, and no finalizer or SSA force-ownership is involved. — EVIDENCE:
spec/04_system-components.md:620 (ownership table row), :625 (gateway RBAC grants),
pkg/gateway/podlifecycle/podsession/slotbinder.go:583-600 (`DrainSandbox`).

DECISION: filed four findings, all spec-internal — the no-retry rule's unlisted edit sites, the
runtime-teardown precondition naming `runtimeLive`, the report/no-report contradiction between
SPEC-2 and SPEC-3, and the incomplete `leaked` predicate. BECAUSE each is a predicate an
implementor reads literally and gets wrong. ALTERNATIVES: I considered and dropped (a) "the new
no-op sentence in the §4.7 row suppresses the occupancy-zero recycle scrub" — the row names
exactly two bolded teardowns, so "runs neither teardown" does not reach the whole-pod scrub, and
the recycle Shutdown does name an already-deregistered session
(pkg/gateway/podlifecycle/podsession/slotbinder.go:562-577); (b) "§5.2's slot-cleanup bullet sits
under a `maxConcurrentSessions > 1` heading so SPEC-3's insert does not reach an exclusive pool" —
spec/05:395 and :453 already make slots and the per-slot cleanup uniform across concurrencies, so
the scoping ambiguity is pre-existing; (c) "§6.2's `lenny_adapter_leaked_slots` equivalence breaks
for a gateway-determined leak" — that divergence already exists in the shipped
`Binder.ReleaseSlot` transport-error arm and is not introduced here.

UNVERIFIED: whether the `receiving_uploads` state is entered on an upload-free plan, where
`stageWorkspace` sends no `PrepareWorkspace`. If the §6.2 edge label "workspace materialization
begins for this slot" is read as PrepareWorkspace rather than as entering the staging stage, a
reclaim on that branch has no legal edge out of `slot_assigned` and SPEC-4 does not cover it. I
judged the charitable reading intended and did not file. Someone with the gateway staging path in
front of them should settle it.

### [spec.1.review-mechanism.1]

FACT: "the pod's shared runtime process has been given <session>" is already bound spec vocabulary for
`runtimeLive`/`runtimeCohort` membership, not for `slotState.started`. The adapter file that owns that state
says so in its header and in noteRuntimeStarted's doc comment, and it explicitly contrasts it with the slot
registry ("The slot registry cannot answer that").
EVIDENCE: pkg/adapter/runtimegeneration.go:8-21, :76-88; spec/04_system-components.md:962;
spec/15_external-api-surface.md:1734

FACT: `st.started` is set inside claimSessionSlotUnderLock BEFORE Runtime.Start, so it is true for a start
still in flight. Any spec sentence that names the teardown precondition as "the runtime has been given the
session" therefore does NOT describe `started`.
EVIDENCE: pkg/adapter/slotsession.go:88; pkg/adapter/session.go:156,163

FACT: the gateway-derived `leaked` disposition already exists end to end — Binder.ReleaseSlot sets
`leaked = err != nil || !cleanly` on the adapter Shutdown, and SlotClaimer.ReleaseSlot(leaked=true) skips the
counter decrement and the claim disposition entirely. Nothing new is needed for §6.2's "leaked holds
occupancy" on the compensation path.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:832-840

FACT: §5.2's slot-cleanup action list is exactly "workspace directory, process group, slotId". It names
neither /run/lenny/slots/{sessionId}/credentials.json nor the §4.9 direct-mode expiry timer, and §4.9 states
the arming obligation with no disarm rule. removeSlotTree does remove the credentials dir
(slotlayout.RemoveTree sweeps CredentialsDir), so the code is ahead of the spec here.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec/04_system-components.md:1169;
pkg/adapter/slotlayout/tree.go:58-68

DEFERRED [proposals/0081.../non-spec-changes.md CODE-2]: the noteRuntimeStarted guard does not close the
other ordering of the same race. If the compensating Shutdown lands BEFORE claimSessionSlotUnderLock runs,
the reclaim removes nothing durable: the start's own ensureSlotStateLocked re-inserts the entry, sets
sessionID and started, Runtime.Start succeeds, and noteRuntimeStarted then FINDS a matching entry and returns
true. No rollback fires, and the pod is left holding exactly the third residue class for a session the
gateway has abandoned. The guard as written detects only reclaim-after-claim. What is true instead: the
adapter needs a discriminator that survives entry re-creation (a per-session reclaim fence/tombstone, or the
start re-checking a generation captured before the claim), or §7.1 must not promise "so the pod holds no
state for that session". Remedy is code-lane, so this loop cannot land it.
EVIDENCE: pkg/adapter/slot.go:97-126 (ensureSlotStateLocked inserts unconditionally);
pkg/adapter/slotsession.go:75-90; non-spec-changes.md:170-200

WATCHOUT: do not read "The pre-attached disposition governs the pod; this reclaim governs the slot state on a
pod that is released or reused rather than terminated" (spec-changes.md:148) as scoping the reclaim away from
pods that retire. It cannot be: on the concurrent path a failed bind whose slot was the pod's last occupant
also retires the pod (ReleaseSlot deletes the claim at remaining==0), and the proposal compensates there.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:880-885

OPEN: after the split, a registered-but-unbound co-tenant still does not hold off the §15.4.2 drain
(`boundRemains` reads the binding), so a Shutdown of the last started session can signal the shared runtime
to terminate while another session is mid-bind and about to StartSession against it. The proposal names this
as pre-existing and does not stage it; nobody has decided whether it stays out of scope once the residue is
gone. EVIDENCE: pkg/adapter/session.go:238,259-261; pkg/adapter/slotsession.go:181-186

### [spec.1.review-operational.1]

FACT: `lenny_adapter_leaked_slots` is emitted by the GATEWAY, not the adapter, despite §6.2 calling it adapter health metadata. `applySlotRetryPolicy` calls `slots.MarkLeaked` + `leakGauge` on a failed release, and the gauge is registered in gateway metrics. Do not build a finding on "the adapter cannot see this leak" — the gateway owns the gauge. EVIDENCE: pkg/gateway/sessionserver/start.go:2844-2846, pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-224, spec/06_warm-pod-model.md:160
FACT: the adapter's cleanup outcome is derived from the runtime close error alone; the slot-tree removal error is discarded today (`_ = removeSlotTree(st)`), so a tree-removal failure is never reported `leaked` on any path. CODE-1 repurposes `ShutdownResponse.exited_cleanly` as the unstarted path's leak signal, and the spec describes `exited_cleanly` nowhere. EVIDENCE: pkg/adapter/session.go:270-281, pkg/adapter/sessionscrubreporter.go:39-43, schemas/lenny-adapter.proto:1665-1668
FACT: §5.2 has TWO reporting statements. The **Scrub model** paragraph (spec/05:453) states the ReportSessionScrub rule "uniform across session-mode configurations ... on every session release"; the **Slot cleanup** bullet (spec/05:545) is inside a heading scoped `maxConcurrentSessions > 1` (spec/05:542). SPEC-3 edits only the second.
WATCHOUT: the "is a pre-start reclaim a session release?" defence kills a whole family of findings. §4.7's ReportSessionScrub row, §5.2:453, and §12:481 all scope their reporting rule to "a session release", and the proposal deliberately frames the reclaim as not one. Do not file "the withheld report contradicts §4.7/§12" — it gets refuted. What survives is the LEAKED terminal, which §6.2:160 requires to be counted with no scoping to session releases. EVIDENCE: spec/04_system-components.md:692, spec/12_storage-architecture.md:481, spec/06_warm-pod-model.md:160
FACT: §6.2's fence puts `slot_cleanup ──→ leaked` in the block headed "Per-slot sub-states scoped to concurrent occupancy", while SPEC-4 adds its edge to the separate "a pod of either concurrency" block whose only terminal is `released`. Pre-existing asymmetry; I judged it below the bar, but a later round asking why the leaked terminal is unreachable on a single-session pod should know it is structural rather than introduced. EVIDENCE: spec/06_warm-pod-model.md:146-155
DEFERRED [docs/reference/adapter-contract.md]: line 75's `Shutdown` row states the shipped one-teardown contract ("The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`"). After SPEC-1 and SPEC-3 that is false for a reclaim of an unstarted entry: no usage flush, no runtime close, no report. The proposal's staged docs list carries only `docs/reference/state-machines.md`. What is true instead: two teardowns with two preconditions, plus the no-op answer for a session the adapter holds no entry for. EVIDENCE: docs/reference/adapter-contract.md:75, proposal non-spec-changes.md:403-416
UNVERIFIED: whether the recycle boundary can be reached by a failed bind that drives occupancy to zero on a recycling pod. The compensating `Shutdown` carries no recycle disposition and `ReleaseSlotReservation` decrements afterwards, so nobody sends the recycle-disposition `Shutdown` for that transition. This is pre-existing (today's release also decrements with no RPC), so I did not file it; a mechanism reviewer should decide whether the proposal widens it.

### [spec.1.review-performance.1]

FACT: `ReportSessionScrub` is the ONLY adapter→gateway channel carrying a per-slot cleanup
outcome, and it carries two things at once: `IncrementSessionsServed` (the
`recycle.maxSessionsPerPod` retirement) and `DrainLedger.RecordSessionScrub(..., leaked)`
(the §5.2 unhealthy-threshold ledger). Suppressing the report to protect the served-session
count also suppresses the leak signal. — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:44-46,:185-190;
pkg/adapter/session.go:277-282

FACT: `ShutdownResponse` has exactly one bool, `exited_cleanly`, with no doc comment in the
proto, and the adapter sets it from the runtime close alone (`ExitedCleanly: closeErr == nil`)
while discarding the slot-tree removal error (`_ = removeSlotTree(st)`). Nothing in spec/
defines the field's meaning. Programme rule S-2 bars opening the proto, so the response bit
cannot be widened by a field. — EVIDENCE: schemas/lenny-adapter.proto:1665-1666;
pkg/adapter/session.go:271, :291

FACT: `SlotClaimer.ReleaseSlot(..., leaked=true)` already skips the Redis decrement and the
occupancy-zero disposition, so "release the reservation with the leaked disposition" does hold
occupancy. Do not file the staged §7.1 "releases the slot reservation afterwards" as
contradicting the leaked hold; the code makes them compatible. — EVIDENCE:
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836

FACT: today a bind failure at `maxConcurrentSessions: 2` already drains the pod, because the
windowed `RecordFailure` alone reaches `ceil(2/2)=1`. The new persistent-leak disposition
therefore changes pod churn only at concurrency >= 3, which is what the non-spec staging
claims. The churn trade is argued and accepted; it is a design preference rather than a
finding. — EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2860; spec/06_warm-pod-model.md:160

WATCHOUT: the leaked-occupancy hold has no durable backing. The Redis `active_slots` counter's
only fallback and rehydration source is `SessionStore.GetActiveSlotsByPod` over Postgres rows
with `state='active'`, and a bind that failed has no active row, so a Redis restart or the
§12.4 outage fallback recomputes occupancy without the leak. I did NOT file this: the same hole
already exists for the shipped leaked class (a completed session's row is not `active` either),
so it is pre-existing rather than introduced. A later round wanting to close it should widen
§5.2's rehydration text, not §7.1. — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:551; spec/12_storage-architecture.md:219;
spec/07_session-lifecycle.md:23

UNVERIFIED: whether §5.2's `**Slot failure and cleanup**` bullet, headed
`(maxConcurrentSessions > 1)`, is meant to govern the slot release on a `maxConcurrentSessions: 1`
pod at all. The staged §4.7 row defines the slot release as "the §5.2 slot cleanup" with no
concurrency scoping, and the staged §6.2 edge lands in the "a pod of either concurrency" block
whose only terminal is `released` (the `slot_cleanup ──→ leaked` edge lives in the
concurrency-scoped block above it). Someone should decide whether that is a real predicate
drift or a pre-existing scoping quirk. — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:542; spec/06_warm-pod-model.md:145-155

UNVERIFIED: the staged Design paragraph says the slot release "reclaims the slot's per-slot
tree, its credential file, its armed §4.9 expiry timers, and the `slotId`" and then defers the
action list to §5.2, which lists three items and names neither the credential file nor the
timers. The code cancels the timers inside `deregisterSlotLocked` regardless, so behaviour is
right and only the spec's action list is short. A citations-lens reviewer should decide whether
that deferral is a false attribution. — EVIDENCE:
spec-changes.md:16-19, :123-125; spec/05_runtime-registry-and-pool-model.md:545

### [spec.1.review-reliability.1]

FACT: every verbatim quote in the staged edits matches the tree at this commit. §4.1 sentence
spec/04_system-components.md:157; §4.7 `Shutdown` row spec/04_system-components.md:686; §4.7.9 step 5
spec/04_system-components.md:853; §5.2 slot-cleanup bullet spec/05_runtime-registry-and-pool-model.md:545;
§6.2 per-slot fence spec/06_warm-pod-model.md:150-155; §7.1 atomicity paragraph
spec/07_session-lifecycle.md:23. Do not re-verify these; spend the budget on semantics.
EVIDENCE: spec/04_system-components.md:157,686,853

FACT: `started` is set in `claimSessionSlotUnderLock` BEFORE `Runtime.Start`, so the compensating
`Shutdown` that lands mid-start takes the STARTED branch, runs `Runtime.Close`, and calls
`reportSessionScrub`. The staged CODE-2 rollback then calls `reportSessionScrub` a second time for the
same session. `RecordSessionScrub` discards the session id and increments `sessions_served`
unconditionally, so the two reports double-advance the `maxSessionsPerPod` retirement the staged §5.2
exception exists to protect. This is the loop's finding 1.
EVIDENCE: pkg/adapter/slotsession.go:87-88; pkg/adapter/session.go:279;
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-460

FACT: `SlotClaimer.ReleaseSlot` with `leaked=true` returns early: no counter decrement, no claim
DELETE, no recycle signal. So the staged `leaked` disposition removes the pod-retirement path the
problem statement names as today's mitigation (remaining==0 → DeleteClaim → drain). The pod is then
reclaimed only by the §4.6.1 claim orphan GC or by reaching `ceil(maxConcurrentSessions/2)`. I did NOT
file this: §6.2 already states the disposition, `ClaimGarbageCollector` is a real reclaimer, and the
lens bars "merely slower to recover". A later round that wants to reopen it needs a case where nothing
reclaims at all.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836; pkg/controller/warmpool/gc_test.go:108-127

FACT: today `applySlotRetryPolicy` retries even when the release leaked. Its only no-retry conditions
are `reason.NonRetryable()` (exactly §5.2's OOM / workspace_validation / policy_rejection) and budget
exhaustion. The staged §7.1 "the failed attempt is not retried" is therefore a genuinely new rule and
§5.2's closed non-retryable list is not staged for it. This is the loop's finding 3.
EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2876; pkg/gateway/podlifecycle/podsession/slotfailure.go:41-48;
spec/05_runtime-registry-and-pool-model.md:556-559

OPEN: after a Redis restart the slot counter is rehydrated from `SessionStore.GetActiveSlotsByPod`,
which counts active sessions and therefore cannot see a leaked slot's held occupancy. Every leaked
slot's occupancy silently disappears on rehydration and the gateway can over-assign into its
unreclaimed resources. This is pre-existing (cleanup-timeout leaks already reach it) so I did not file
it, but this proposal makes `leaked` the routine outcome of an unacknowledged reclaim, which raises its
frequency by a lot. Somebody should decide whether it wants its own problem statement.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:565 (post-recovery rehydration atomicity)

WATCHOUT: the compensation is sent only after the failing RPC returned, but a workspace-prep handler
whose client deadline already expired still runs `ensureSlotPaths` (no ctx check) and can insert the
registry entry AFTER the reclaim was answered as a no-op. `noteRuntimeStarted` guards only the
`StartSession` half of this race. I did not file it: the remedy is an adapter-side tombstone, which the
summary rejects as a non-goal, and no fix lands in the staged spec. A code-lane reviewer should look.
EVIDENCE: pkg/adapter/staging.go:134,181; pkg/adapter/slot.go:135-148

UNVERIFIED: the tier-7a race case asserts "the runtime was closed exactly once" while the staged design
has two `Runtime.Close` calls in that race (the Shutdown's started branch and CODE-2's rollback). It
holds only because `SocketRuntimeProcess.Close` early-returns on `!p.connected`; `InProcessRuntime` and
`MCPRuntime` have no such guard. The non-spec loop should check that assertion against the runtime the
test uses.
EVIDENCE: 0081...non-spec-changes.md:548-553; pkg/adapter/socketruntime.go:437-446

### [spec.1.review-security.1]

FACT: `podclaim.SlotClaimer.ReleaseSlot` returns early on `leaked=true`, skipping BOTH the Redis
decrement AND the occupancy-zero disposition, so a leaked release never reaches the
`DeleteClaim` that retires the pod today. — EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836,
:882-885

FACT: today's `ReleaseSlotReservation` hard-codes `ReleaseSlot(ctx, sandboxName, false, false)`, so a
failed bind on a pod with no co-tenant deletes the claim and the pod retires — this is the safety valve
the problem statement relies on ("the residue dies with it"). Threading the leaked disposition through it
(CODE-4/CODE-5) removes that valve in exactly the failure case. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503

FACT: the §5.2 leak/failure ledger is a purely in-process, per-replica map (`Tracker.leaked`,
`Tracker.events`), with `UnhealthyThreshold = (maxConcurrent+1)/2`. It is not Redis- or Postgres-backed,
so the retirement path the staged text leans on is replica-local and non-durable. — EVIDENCE:
pkg/gateway/runtime/slothealth/slothealth.go:56-67, :215-221

FACT: `RecordSessionScrub` does two things at once — increments `sessionsServed` and, on `leaked`, feeds
the drain ledger. Any spec rule that withholds `ReportSessionScrub` withholds both. — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:37-47

FACT: the adapter's `Resume` claims the session's slot and reaches `Runtime.Start` +
`noteRuntimeStarted`, so a lost or cancelled Resume leaves the same class-3 residue (started entry,
running runtime for an abandoned session) as a lost StartSession. — EVIDENCE: pkg/adapter/resume.go:50,
:140-144

FACT: `removeSlotTree` covers `CredentialsDir`, so the §5.2 action list ("removes the slot's workspace
directory") does reach `/run/lenny/slots/{id}/credentials.json`; the armed §4.9 expiry timers are
cancelled inside `deregisterSlotLocked`, which runs for any removed entry. Neither needs its own spec
sentence. — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68, pkg/adapter/slot.go:210-212

WATCHOUT: do NOT file "the staged §7.1 rollback omits credential-lease release". §7.1 step 23 is the
existing spec basis and `failPhase` already cites it for the pre-attached failure path, so the code
staging's `releaseCredentials` is anchored. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1065-1078,
:1259-1268

WATCHOUT: the "PrepareWorkspace handler inserts the entry after the reclaim's deregister" race looks
attractive but is not real enough to file: both `resolvePrepareStagingDir` and `FinalizeWorkspace` call
`ensureSlotPaths` at the top of the handler, before the slow work, so a client-side deadline almost
always postdates the insert. — EVIDENCE: pkg/adapter/staging.go:133-143, :158-183

OPEN: SPEC-3 withholds `ReportSessionScrub` entirely for the pre-start reclaim, so a pre-start cleanup
that FAILS on the pod can never be reported `leaked` and never reaches the §5.2 threshold or the
`lenny_adapter_leaked_slots` gauge. I did not file it: today `removeSlotTree`'s error is already
discarded (`_ = removeSlotTree(st)`, pkg/adapter/session.go:271) and the leaked outcome is derived from
`closeErr` alone, so the staged rule removes nothing that exists. Someone should decide whether the
spec wants the report suppressed only for `released`, keeping the `leaked` signal.

OPEN: spec/05:455 and :492 both say "A session that ends in failure or a crash always retires its pod
regardless of recycle settings", while spec/06:283 (pre-attached failure retry policy) says the pod "is
marked `failed` and released back to the pool (or terminated if unhealthy)". That tension predates this
proposal; I did not file it, but a fix for finding 1 should not deepen it.

UNVERIFIED: spec/29 item 12 enumerates the gateway→adapter `Shutdown` scenario as "On a session end
triggered by terminate/DELETE/expiry". This proposal adds a new `Shutdown` trigger (the failed-bind
reclaim) that item 12 does not cover. Item 12 is not falsified, so I left it; an edit-site lens should
decide whether §29's enumeration is meant to be exhaustive. — EVIDENCE: spec/29_communication-scenarios.md:694-702

### Pass 2 — spec loop, round 2

The round-2 fixers recorded their corrections in their own shards and opened no pass
subsection here, so this one opens it and carries that pass's corrections. The bullets below
are follow-up corrections to round 2 rather than a new round.

- **The 0080 §1.19 impact row stated one membership case out of two.** Round 2 deleted §7.1's
  promise that a reclaimed session leaves the pod holding nothing and replaced it with a
  two-ordering statement, so the row's claim that a fence for a failed bind meets the
  absent-entry refusal is no longer exhaustive. In the ordering where the racing start's own
  claim re-creates the entry, `ensureSlotStateLocked` re-inserts it and
  `claimSessionSlotUnderLock` sets `sessionID`, so `boundSlotState` finds a present, bound
  entry and refuses nothing (`pkg/adapter/slotsession.go:75`, `:87-88`, `:274-283`). The row
  now states both cases and no longer asserts that the change narrows the problem, which the
  second case does not support. Summary.md's grant covers the row because a round-2 edit
  falsified it.
- **Four standing DEFERRED entries carried text round 2 falsified.** The S3 entry still
  described SPEC-3's range as "before its slot reaches `running`", which round 2 narrowed to
  "after its slot enters `receiving_uploads` and before it reaches `running`" in the four
  sites that hold it. The S8 entry still stated the racing-start guard as total. The S2 entry
  still named a pod-disqualification rule and a racing-start rule inside §7.1, both of which
  round 2 moved or deleted. The CODE-2 entry offered "or §7.1 must not promise" as an
  untaken branch that round 2 took. All four are corrected in place in the standing register
  above; the archived shard copies below keep the words they were written with.

- DEFERRED · implementation-checklist.md S8 (line 20) is the site the round-2 correction was
  handed and could not reach: this round's hard constraint puts the implementation checklist
  out of bounds for both fixers. The corrected text is in the standing register above.
- DEFERRED · implementation-checklist.md S2 (line 8) still lists a racing-start rule and a
  no-retry rule among what §7.1 gains, and the deliverable index at summary.md now disagrees
  with it. Corrected text in the standing register above. Same out-of-bounds cause.
- DEFERRED · implementation-checklist.md S3 (line 10) still carries SPEC-3's pre-narrowing
  range. Corrected text in the standing register above. Same out-of-bounds cause.
