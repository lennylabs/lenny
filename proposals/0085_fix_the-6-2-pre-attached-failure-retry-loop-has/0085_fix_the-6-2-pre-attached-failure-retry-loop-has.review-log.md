# Review log: The §6.2 pre-attached retry loop has no stop condition for a terminal session

## Standing context

### Settled

### Traps

### Open

### Deferred

## Ledger


### [spec.1.fix-G1.1]
DECISION: SPEC-1 Scope pre-persist unit is `POST /v1/sessions/start` (plus MCP/adapter paths) alone; persisted-row examples are finalize and delegated-child materialization — BECAUSE POST /v1/sessions persists at §7.1 step 5 right after the step-4 claim and runs no setup; /{id}/start launch and slot setup are post-attached in `starting` under approved 0105; /resume only writes resume_pending and the §7.3 Resume driver re-claims — ALTERNATIVES: "persists only after its pre-persist steps succeed" (would pull the POST /v1/sessions claim into the re-claim retry, against §7.1 fail-fast); keeping /resume with a §7.3 parenthetical (the trailing "claims no replacement pod" sentence applies to every listed request).
DECISION: Design section keeps one pointer sentence to the SPEC-1 Scope bullet; rules 1 and 2 deleted — BECAUSE a corrected copy is still a second home of the predicate.
WATCHOUT: `RESUME_FAILED` no longer exists anywhere in spec/ or docs/ (0092, commit 64ab2faba, removed it); do not restore it in SPEC-2 or edge cases — EVIDENCE: spec/15_external-api-surface.md §15.1 SETUP_COMMAND_FAILED row reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]"; same text on impl/0105-session-timers.
WATCHOUT: Do not re-add `/{id}/start` to the Scope list or cite `starting → resume_pending`/§7.3 for it. 0105 (spec landed on impl/0105-session-timers) deletes that edge and rewrites the §6.2 "sole exception" sentence; the edge case points at the "sole exception" sentence because that pointer is true before and after 0105 lands. Pre-0105, a concurrent-workspace /start slot setup is still covered by the general persisted-row sentence ("Such requests include" is non-exhaustive) — EVIDENCE: 0105 spec-changes lines 163/169.
FACT: 0092 summary Impacts row for 0085 asked 0085 to drop /resume and RESUME_FAILED — EVIDENCE: proposals/0092_*/0092_*.summary.md:174.
FACT: 0086 spec-changes does not touch the §6.2 Scope bullet or the §15.1 SETUP_COMMAND_FAILED fallback clause, so SPEC-1/SPEC-2 anchors are unaffected by 0086 — EVIDENCE: grep of proposals/0086_*/0086_*.spec-changes.md for "Retries apply per client" and "fallback and is recovered" returns nothing.
OPEN: The summary's "Records numbering note" (F-6.2.27 renumber) is a caller directive but not in this group's findings; RECORDS-1 lives in non-spec-changes, which this lane may not author in. A later lane must apply it.


### [spec.1.fix-G2.1]
FACT: The §28.5.3 **Session frame writes.** table row "`DemoteSDK` whose wait for the slot serialization outlasts its request's deadline" said "A gateway caller then fails the pod and claims a replacement" and cited the §4.7.1 `ConfigureWorkspace` row. SPEC-3 Edit (c) now deletes " and claims a replacement" and keeps the citation, because the §4.7.1 row still states the pod failure after Edit (a). No other proposal (0082, 0086, 0105, 0106, 0107) edits that row. EVIDENCE: spec/28_communication-channels.md:693 (table label), :709 (row)
FACT: §29.2 step 23 now reads "Steps 24 through 32", after the renumbering by 0087/0090. SPEC-3 Edit (b) now quotes only the two lines that carry the replacement clause, so the step range is outside the edit. EVIDENCE: spec/29_communication-scenarios.md:286-288
WATCHOUT: Do not bring back a post-landing grep completeness check for the replacement-claim sites. The spellings differ: "a replacement is claimed" in §4.7.1, the same phrase wrapped across two lines in §29.2, and "claims a replacement" in §28.5.3. A broad pattern also matches legitimate resume-path claims: §7.3 step 3b ("claims a replacement pod") and the §6.2 resume_pending sentence ("a replacement pod is claimed"). Each edit's own find/replace text already verifies it. EVIDENCE: spec/07_session-lifecycle.md:449, spec/06_warm-pod-model.md:180
MISTAKE: The original SPEC-3 closing grep check expected a §6.2 hit that its pattern could never match. It never matched the wrapped §29.2 occurrence either. Round 1 deleted it.
DECISION: Edit (c) is part of SPEC-3 rather than a new SPEC-4 — BECAUSE it is the same replacement-claim clause deletion, and SPEC-2 and SPEC-3 delete clauses rather than naming a governing policy — ALTERNATIVES: rewording the row to defer to §6.2 Scope (restates the rule); dropping the §4.7.1 citation (it still supports "fails the pod").
DEFERRED [proposals/0085_fix_the-6-2-pre-attached-failure-retry-loop-has/0085_fix_the-6-2-pre-attached-failure-retry-loop-has.implementation-checklist.md]: Step S3 reads "SPEC-3. Lands the §4.7 `ConfigureWorkspace` row and §29.2 step 23 edits." That is now incomplete, because SPEC-3 also has Edit (c) on the spec/28_communication-channels.md §28.5.3 `DemoteSDK`-deadline row. Change S3 to "SPEC-3. Lands every SPEC-3 edit."


### [spec.1.fix-design-G1.1]
DECISION: SPEC-1 Edit (b) Scope text names the retry's unit as a closed positive enumeration ("applies only to the §7.1 creation of `POST /v1/sessions/start`, including the MCP and adapter paths ..., which persists the session row only after the claim, prepare, and launch succeed"), and the persisted-row list shrinks to finalize and delegated-child materialization — BECAUSE the predicate "while the request has persisted no session row" literally admits the `POST /v1/sessions` step-4 claim (it precedes the step-5 persist), which §7.1 Atomicity requires to fail fast; a closed enumeration excludes it and excludes `/{id}/start` whether or not 0105 has landed — ALTERNATIVES: keep the predicate and only drop `POST /v1/sessions` (leaves the create.go retry trap, so the summary watch-out would have to stay); keep `/resume` with a §7.3 parenthetical (the trailing "one attempt, no replacement pod" sentence still binds it and contradicts the §7.3 resume driver's re-claim).
FACT: After 0092 `RESUME_FAILED` appears nowhere in spec/ or docs/; the §15.1 `SETUP_COMMAND_FAILED` row reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]" — EVIDENCE: spec/15_external-api-surface.md:1137. 0105 does not edit that row or the §6.2 **Scope:** bullet (impl/0105-session-timers spec/06:303).
FACT: On impl/0105-session-timers the §6.2 sole-exception sentence reads "the `POST /v1/sessions/{id}/start` launch, which runs after the session has entered the externally visible `starting` state and takes the `starting` edges of [§7.2]", and §7.2 has no `starting → resume_pending` edge — EVIDENCE: git show impl/0105-session-timers:spec/06_warm-pod-model.md:306, :spec/07_session-lifecycle.md:180-182, :232.
FACT: §7.1 persists the `POST /v1/sessions` row at step 5, right after the step-4 claim; prepare and launch run later against the persisted row — EVIDENCE: spec/07_session-lifecycle.md:19-20, :22.
WATCHOUT: 0092 CODE-4 already deleted `holdOrFailOnResumeError` and `isTransientPodClaimError` (grep of pkg/ finds no definition), so 0085 non-spec CODE-1 (d) table rows targeting their doc comments locate nothing. Not in G1; a non-spec-loop finding — EVIDENCE: non-spec-changes.md CODE-1 (d) rows for `start.go`; 0092 summary Impacts row for 0081/0091.
OPEN: problem-statement.md "Refuted for the create paths" bullet says `POST /v1/sessions` persists only after claim, prepare, and launch, which §7.1 step 5 contradicts. It is a validated record and was wrong before G1; a later round decides whether a problem statement is corrected.
### [spec.1.fix-design-G1.1]
DECISION: SPEC-1 Scope names only `POST /v1/sessions/start` (plus the MCP `create_and_start_session` and adapter paths) as the retry unit, and only finalize and delegated-child materialization as persisted-row examples — BECAUSE `POST /v1/sessions` runs no setup before its step-5 persist (§7.1 governs its claim failure), 0092 left `/resume` with no pre-attached work (the §7.3 resume driver restores), and 0105 makes the `/{id}/start` launch and slot setup post-attached in `starting` — ALTERNATIVES: "persists the row only after its pre-persist steps succeed" (keeps POST /v1/sessions in the retry, so the claim-wrap trap and edge case 5 have to stay); keeping /start or /resume with a parenthetical (the sentence after the list still applies to every item on it).
DECISION: With /start out of Scope, SPEC-1 no longer depends on the order 0085 and 0105 land in. Point the /start edge case at the §6.2 Client visibility "sole exception" sentence, which is true before and after 0105, rather than at "§7.2 `starting` edges" — BECAUSE the draft-0085 tree (153a7e23b) still carries the pre-0105 `starting_session → resume_pending` sentence.
FACT: The §15.1 SETUP_COMMAND_FAILED row in both draft-0085 and impl/0105-session-timers reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)." 0092 already removed RESUME_FAILED. 0086, 0105, 0106, and 0107 do not edit that clause — EVIDENCE: spec/15_external-api-surface.md (SETUP_COMMAND_FAILED row); grep of the 0086/0105/0106/0107/0082 spec-changes for "it stays the retryable" returns nothing.
FACT: The non-spec text already treats `POST /v1/sessions/start` as the only retry unit (CODE-1 comment "only the POST /v1/sessions/start atomic unit also retries it internally", and F-6.2.27 Impl "`POST /v1/sessions` is not part of this gap") — EVIDENCE: non-spec-changes.md:60, :93
WATCHOUT: The §6.2 Client visibility bullet (spec/06:302) still lists `POST /v1/sessions` claim failures under "on exhaustion". It stays true as a list of which endpoint surfaces which failure, and SPEC-1's exhaustion-outcome sentence covers only persisted-row requests. Do not edit Client visibility. Summary non-goal :57 rejects that edit.


### [spec.1.fix-design-G2.1]
DECISION: SPEC-3 gains Edit (c) on spec/28 §28.5.3, replacing "A gateway caller then fails the pod and claims a replacement" with "A gateway caller then fails the pod"; the §4.7.1 `ConfigureWorkspace` citation stays because that row still states the pod failure — BECAUSE the row cites §4.7.1 as authority for the replacement claim SPEC-3 deletes there — ALTERNATIVES: rewording the row to defer to §6.2 Scope (rejected: restates the Scope rule, contrary to the summary decision that SPEC-2/SPEC-3 delete rather than name a policy).
DECISION: Edit (b) blocks drop their third line (the "Steps 24 through N" line) instead of updating 29 to 32 — BECAUSE the replaced text is wholly in the first two lines, and dropping the step-range line removes the edit's dependence on §29.2 numbering, which other proposals keep renumbering.
DECISION: the SPEC-3 grep check sentence is deleted rather than rewritten — BECAUSE no single literal covers the three spellings, and "claims a replacement" also matches the legitimate §7.3 resume-driver sentence (spec/07 §7.3 step 3b), so any rewritten check needs exceptions.
FACT: the only spec sites stating a replacement claim after a DemoteSDK failure are spec/04 §4.7.1 `ConfigureWorkspace` row, spec/29 §29.2 step 23 (wrapped across two lines, so line grep never matched it), and spec/28 §28.5.3 `DemoteSDK`-deadline row. The §6.2 resume_pending sentence reads "a replacement pod is claimed" — EVIDENCE: spec/04_system-components.md:673, spec/29_communication-scenarios.md:286-287, spec/28_communication-channels.md:709, spec/06_warm-pod-model.md:180
FACT: no unlanded proposal (0082, 0086, 0105, 0106, 0107) edits the §28.5.3 `DemoteSDK`-deadline row; 0107 SPEC-10 edits §28.5.2 only — EVIDENCE: grep of proposals/*/spec-changes.md
WATCHOUT: the ConfigureWorkspace row sits under §4.7.1 Role and Gateway RPC Contract, not §4.7; the SPEC-3 heading and Edit (a) label say §4.7. Not this group's finding — EVIDENCE: spec/04_system-components.md:673
DEFERRED [0085_fix_the-6-2-pre-attached-failure-retry-loop-has.implementation-checklist.md]: S3 says it "Lands the §4.7 `ConfigureWorkspace` row and §29.2 step 23 edits"; after Edit (c) it must also land the §28.5.3 `DemoteSDK`-deadline row edit. Better: cut S3 to "SPEC-3. Lands every SPEC-3 edit." so it no longer lists sites.


### [spec.1.review-applicability.1]
FACT: The §15.1 SETUP_COMMAND_FAILED row no longer names RESUME_FAILED; 0092 (commit 64ab2faba) removed it and added "A setup command that fails during a resume is not returned to a caller". Current fallback text is "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]". EVIDENCE: spec/15_external-api-surface.md:1137
FACT: §29.2 step 23's third anchor line now reads "Steps 24 through 32", not 29 (0090 SPEC-7, commit 67968d806). EVIDENCE: spec/29_communication-scenarios.md:287
FACT: §6.2 line 180 says "a replacement pod is claimed", so `grep "a replacement is claimed" spec/` returns nothing after SPEC-3. EVIDENCE: spec/06_warm-pod-model.md:180
FACT: 0086, 0105, 0106, 0107 stage no edit at any 0085 site (§6.2 retry policy, §15.1 SETUP_COMMAND_FAILED row, §4.7 ConfigureWorkspace row, §29.2 step 23); 0082 is Implemented. EVIDENCE: grep over proposals/{0086,0105,0106,0107}_*/*spec-changes.md
WATCHOUT: Re-quote every 0085 anchor against the current tree; the draft predates 0090 and 0092 landings.


### [spec.1.review-citations.1]
FACT: Proposal 0092 (Implemented) deleted the §15.1 `RESUME_FAILED` row and made `POST /v1/sessions/{id}/resume` a SessionStore write (`resume_pending`) that the §7.3 resume driver acts on; `RESUME_FAILED` has zero hits under spec/. 0092's summary row for 0085 says to drop `/resume` from the persisted-row list and `RESUME_FAILED` from anchors. — EVIDENCE: spec/15_external-api-surface.md:648 (resume row), :1137 (SETUP_COMMAND_FAILED row reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback"); proposals/0092_*/0092_*.summary.md:174
WATCHOUT: §29.2 step 23 now ends "Steps 24 through 32 are the pod-warm startup", not "24 through 29"; the "a replacement is claimed" phrase wraps across lines there, so a single-line grep never matched it. — EVIDENCE: spec/29_communication-scenarios.md:286-288
FACT: §6.2's resume_pending re-dispatch sentence says "a replacement pod is claimed" (not "a replacement is claimed"). — EVIDENCE: spec/06_warm-pod-model.md:180
FACT: §7.1 POST /v1/sessions persists the row at step 5 right after the step-4 claim; prepare runs at finalize and launch at start, both after persist. — EVIDENCE: spec/07_session-lifecycle.md:5-23
FACT: 0105, 0106, 0107 spec-changes do not touch the §6.2 retry policy, the SETUP_COMMAND_FAILED fallback clause, or "a replacement is claimed"; 0086 SPEC-5 edits §29.2 steps 14/15/17 only, not step 23.


### [spec.1.review-client-surface.1]
FACT: `RESUME_FAILED` is not an error code anywhere in spec/ (grep returns nothing); 0092 (commit 64ab2faba) removed it from the §15.1 `SETUP_COMMAND_FAILED` row, which now reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]". SPEC-2's anchor text quoting a three-code list does not match the tree. — EVIDENCE: spec/15_external-api-surface.md:1137
FACT: `POST /v1/sessions/{id}/resume` runs no pre-attached work in the request: it returns 200 with the row in `resume_pending` and the resume driver restores asynchronously, claiming a replacement pod and re-attempting after a claim failure. Any 0085 text saying the gateway "makes one attempt, claims no replacement pod, and returns that attempt's failure" must not cover /resume. — EVIDENCE: spec/15_external-api-surface.md:648; spec/07_session-lifecycle.md:447-449
FACT: POST /v1/sessions persists the row at §7.1 step 5 right after the claim; prepare runs at finalize. Only POST /v1/sessions/start persists after claim, prepare, and launch. — EVIDENCE: spec/07_session-lifecycle.md:17-18; pkg/gateway/sessionserver/create_and_start.go:393-402
FACT: docs/reference/error-catalog.md:128 `SETUP_COMMAND_FAILED` row carries no "fresh pod" clause, so SPEC-2 has no docs mirror to propagate.


### [spec.1.review-docs-alignment.1]
FACT: RESUME_FAILED is gone from spec/, docs/, and the error classifier (proposal 0092, Implemented). The §15.1 SETUP_COMMAND_FAILED row now reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]" and says a resume-time setup failure is not returned to a caller. — EVIDENCE: spec/15_external-api-surface.md:1137; pkg/gateway/externalapi/errorclassify/errorclassify_test.go:209-217
FACT: POST /v1/sessions/{id}/resume only writes resume_pending; the §7.3 Resume driver claims replacement pods and retries a failed claim later. Any "/resume makes one attempt, claims no replacement pod" text contradicts it. — EVIDENCE: spec/07_session-lifecycle.md:447, :449
FACT: §29.2 step 23 now ends "Steps 24 through 32 are the pod-warm startup" (renumbered by 0087), not "24 through 29". — EVIDENCE: spec/29_communication-scenarios.md:288
FACT: The ConfigureWorkspace row lives under §4.7.1 Role and Gateway RPC Contract, at spec/04_system-components.md:673.
FACT: docs/ describes no gateway-internal pre-attached retry at finalize, /{id}/start, resume, or delegation. docs/reference/error-catalog.md:154-155 describes only a client retry that rebinds a fresh pod, and that stays true after 0085. No docs edit is owed. — EVIDENCE: lookup exact "pre-attached|re-claim" over docs returned no retry claims
WATCHOUT: 0085's staged old-text blocks were drafted before 0087 and 0092 landed. Re-diff every Replace block against the current tree. — EVIDENCE: SPEC-2 and SPEC-3(b) both fail to match


### [spec.1.review-edit-sites.1]
FACT: The §15.1 `SETUP_COMMAND_FAILED` row no longer lists `RESUME_FAILED` in its fallback; proposal 0092 (commit 64ab2faba) removed it and added "A setup command that fails during a resume is not returned to a caller". SPEC-2's quoted old text is stale. — EVIDENCE: spec/15_external-api-surface.md:1137
FACT: §29.2 step 23 now ends "Steps 24 through 32 are the pod-warm startup", not "24 through 29"; SPEC-3 edit (b)'s old and new text are stale. — EVIDENCE: spec/29_communication-scenarios.md:288
WATCHOUT: "a replacement is claimed" grep misses the §28.5.3 `DemoteSDK` row, which says "claims a replacement" and cites the §4.7.1 ConfigureWorkspace row SPEC-3 edits. Search for "claims a replacement" too. — EVIDENCE: spec/28_communication-channels.md:709
FACT: 0086 SPEC-7 makes §7.5 defer setup-failure outcome to §6.2, and 0105 edits the §7.2 visibility paragraph; neither touches 0085's staged anchors. — EVIDENCE: proposals/0086_*/...spec-changes.md SPEC-7 edit (a); proposals/0105_*/...spec-changes.md:71


### [spec.1.review-fresh.1]
FACT: `RESUME_FAILED` no longer exists anywhere in spec/; 0092's spec commit 64ab2faba removed it from the §15.1 `SETUP_COMMAND_FAILED` row, which now reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]". SPEC-2's quoted old text is stale. — EVIDENCE: spec/15_external-api-surface.md:1137; `grep -rn RESUME_FAILED spec` empty
FACT: §29.2 step 23 now ends "Steps 24 through 32" (0087 renumbered, 801d789a3); SPEC-3(b) quotes "24 through 29". — EVIDENCE: spec/29_communication-scenarios.md:289
WATCHOUT: §28.5.3 decision table row "`DemoteSDK` whose wait for the slot serialization outlasts its request's deadline" says "A gateway caller then fails the pod and claims a replacement (§4.7.1, `ConfigureWorkspace` row)" — a third replacement-claim site SPEC-3 misses, and it cites the row SPEC-3 edits. — EVIDENCE: spec/28_communication-channels.md:709
WATCHOUT: 0105 (Approved, spec already landed on branch impl/0105-session-timers, f1bf2f8eb) deletes the `starting → resume_pending` edge, removes `starting` from §7.3 retry accounting, makes /{id}/start (incl. slot setup) post-attached, and rewrites the §6.2 "sole exception" sentence. Check every 0085 mention of /{id}/start against `git show impl/0105-session-timers:spec/...`.
WATCHOUT: `grep "a replacement is claimed" spec/` already matches nothing in §6.2 (line 180 says "a replacement pod is claimed"), so SPEC-3's postcondition sentence is false. — EVIDENCE: spec/06_warm-pod-model.md:180
FACT: §7.1 atomicity: POST /v1/sessions persists the row at step 5 after only pre-check and claim; prepare runs at finalize. SPEC-1's "persists the row only after the claim, prepare, and launch succeed" is true only for /v1/sessions/start. — EVIDENCE: spec/07_session-lifecycle.md:23
FACT: 0082 status is Implemented (proposal-status), not Approved as the summary impacts table says. Non-spec loop owns that.


### [spec.1.review-mechanism.1]
FACT: 0105's spec edits are already landed on branch impl/0105-session-timers (not in this worktree). There §6.2 **Client visibility:** "sole exception" is the `/{id}/start` launch in `starting`, §7.2 classifies `/start` launch incl. slot setup as post-attached, and `starting → resume_pending` is deleted. Check every 0085 /start statement with `git show impl/0105-session-timers:spec/06_warm-pod-model.md` — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md Edit (b) §7.2 and Edit (g) §6.2
FACT: `RESUME_FAILED` no longer exists anywhere in spec/15; the SETUP_COMMAND_FAILED row reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback" and says a resume setup failure is not returned to a caller (resume driver commit 64ab2faba) — EVIDENCE: spec/15_external-api-surface.md:1137
FACT: `POST /v1/sessions/{id}/resume` returns 200 with the row in resume_pending; the §7.3 resume driver claims the replacement pod and re-attempts a failed claim — EVIDENCE: spec/15_external-api-surface.md:648, spec/07_session-lifecycle.md:449
FACT: §29.2 step 23 now reads "Steps 24 through 32" (0087 renumbered) — EVIDENCE: spec/29_communication-scenarios.md:288
WATCHOUT: the glob `proposals/0105_*/*spec-changes.md` also matches non-spec-changes.md; sed over it concatenates both files and line numbers mislead.
WATCHOUT: `grep "a replacement is claimed" spec/` hits only §4.7:673 and §29:286; §6.2:180 says "a replacement pod is claimed", so SPEC-3's post-check expects a hit that does not exist.


### [spec.1.review-performance.1]
FACT: 0085's staged edits only narrow the §6.2 retry (fewer claims, no new store or etcd writes); no top-tier write-rate, hot-key, or failover regression exists under the performance lens, since the shipped gateway runs no §6.2 loop anywhere — EVIDENCE: pkg/gateway/sessionserver/finalize.go single podBinder.Prepare; problem-statement Evidence list
WATCHOUT: the SPEC-3 landing check "grep 'a replacement is claimed' returns only the §6.2 sentence" is wrong: the §6.2 resume_pending sentence reads "a replacement pod is claimed" and does not match, so the grep returns nothing after SPEC-3 — EVIDENCE: spec/06_warm-pod-model.md:180
WATCHOUT: POST /v1/sessions persists the row after the claim (step 5), with prepare/launch at finalize; SPEC-1's "which persists the row only after the claim, prepare, and launch succeed" fits only POST /v1/sessions/start — EVIDENCE: spec/07_session-lifecycle.md:17-18,22


### [spec.1.review-plan-conformance.1]
FACT: The plan assigns 0085 two things. The first is the §6.2 pre-attached retry scope, which SPEC-1 stages. The second is renumbering RECORDS-1's F-6.2.27, which is non-spec. — EVIDENCE: gateway-runtime-comms-remediation.md:3303-3306 ("Proposal 0085 (Draft): the §6.2 pre-attached retry scope ... renumbering to the next free identifier at filing"); see also :3183, :3336, :4081, and :4344, which only sequence it after 0086 and before P2b.
WATCHOUT: The plan's "Test instruction" (expansive tests) and "Drafting rules for P1 to P5" do not bind 0085. They are scoped to the P1-P5 block plus 0092, R20, and the recycling follow-up. 0085 sits in "Queue after P4b". — EVIDENCE: gateway-runtime-comms-remediation.md:2835-2842
FACT: As of round 1 of the spec loop, the next free BUILD-GAPS F-6.2 id is F-6.2.32, because F-6.2.27 through F-6.2.31 exist. — EVIDENCE: BUILD-GAPS.md:5690-5719


### [spec.1.review-reliability.1]
FACT: `RESUME_FAILED` is not an error code anywhere in spec/ or docs/; a setup failure during a resume is not returned to a caller (§7.3 Resume driver item 3; §15.1 SETUP_COMMAND_FAILED row) — EVIDENCE: spec/15_external-api-surface.md:1137, spec/07_session-lifecycle.md:447-451
FACT: POST /v1/sessions/{id}/resume only writes resume_pending and returns 200; the resume driver restores asynchronously and re-attempts a failed claim until maxResumeWindowSeconds — EVIDENCE: spec/15_external-api-surface.md:648, spec/07_session-lifecycle.md:449
WATCHOUT: the SPEC-2 and SPEC-3(b) "replace" strings do not match the current tree (row has no RESUME_FAILED; step 23 says "Steps 24 through 32") — they were copied from 0092/0090 staging, not the landed text. Re-quote from spec/ before editing. — EVIDENCE: spec/15_external-api-surface.md:1137, spec/29_communication-scenarios.md:288
WATCHOUT: §28.5.3 Intra-pod DemoteSDK row restates "fails the pod and claims a replacement" citing the ConfigureWorkspace row; the SPEC-3 grep for "a replacement is claimed" misses it, and §6.2:180 says "a replacement pod is claimed", so the grep check returns nothing — EVIDENCE: spec/28_communication-channels.md:709, spec/06_warm-pod-model.md:180
FACT: POST /v1/sessions creation persists the row after pre-check and claim only (§7.1 step 5); no prepare or launch runs before persist — EVIDENCE: spec/07_session-lifecycle.md:18-22


### [spec.1.review-security.1]
DECISION: no security findings on SPEC-1..3 — BECAUSE the edits only narrow §6.2 retries (persisted-row requests get one attempt, no replacement claim) and delete two "replacement is claimed"/"fresh pod" clauses; the failed-pod disposition sentence ("marked `failed` and released back to the pool (or terminated if unhealthy)") is kept, so no scrub/reuse, RBAC, or tenant control regresses and no bound is relaxed — ALTERNATIVES: none filed.
FACT: SPEC-2's before-text (`SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED`) does not match the current tree, which lacks `RESUME_FAILED`; approved-unlanded 0092 adds it — EVIDENCE: spec/15_external-api-surface.md:1137; proposals/0092*/…spec-changes.md:270. SPEC-2 applies only after 0092 lands.
WATCHOUT: POST /v1/sessions persists the row at §7.1 step 5 right after the claim (no prepare/launch on that route); only POST /v1/sessions/start persists after claim+launch (mintClaimStartPersist). SPEC-1(b)'s clause "persists the row only after the claim, prepare, and launch succeed" fits /start only — EVIDENCE: spec/07_session-lifecycle.md:16-17; pkg/gateway/sessionserver/create.go:322-415; create_and_start.go:87. Not filed under the security lens; a correctness lens should judge it.


### [spec.1.review-single-source.1]
FACT: The persisted-row scope rule is staged once in spec text (SPEC-1 edit (b)); the spec-changes "Design" section rules 1 and 2 restate it implementably (one attempt, no replacement claim, failure = exhaustion outcome, the four named persisted-row requests). Filed under (g); remedy is to cut Design to a citation of SPEC-1. EVIDENCE: proposals/0085_*/0085_*.spec-changes.md:12-13 vs :61
FACT: §7.1 and §7.2 already defer to the §6.2 policy rather than restating it, so SPEC-1 copies no other spec section. EVIDENCE: spec/07_session-lifecycle.md:22, :226
WATCHOUT: The edge case about a /start launch failure in `starting` appears in three places: the spec-changes Design bullet, Edge case 2, and summary "Watch out for" 4. Each one cites the §6.2 "sole exception" sentence and the §7.2 edges without restating them, so the single-source lens did not file them.


### [spec.2.fix-G1.1]
DECISION: SPEC-1 Edit (b) Scope text now opens "The retry applies only to the [§7.1] creation of `POST /v1/sessions/start`, ..."; the predicate "while the request has persisted no session row:" is deleted — BECAUSE that predicate admits the `POST /v1/sessions` step-4 claim (it precedes the step-5 persist, spec/07_session-lifecycle.md:22 **Atomicity of session creation (steps 2–8).**), so predicate and list named different sets; "only" excludes `POST /v1/sessions` without naming it and §7.1 Atomicity owns its outcome (roll back, 503 SESSION_CREATION_FAILED) — ALTERNATIVES: adding `POST /v1/sessions` to the persisted-row group (false: no row before its claim); narrowing the predicate (same failure class); generalising "every other request makes one attempt" (contradicts §7.3 resume driver and §5.2 Slot retry policy).
MISTAKE: round 1 recorded a DECISION to make the Scope a closed list (spec.1.fix-design-G1.1) but staged text kept the predicate before the colon; the defect was the predicate, never the list. Cost: a round-2 design-defect finding.
WATCHOUT: do not reintroduce any general condition ("pre-persist", "no session row persisted", "before the row exists") as the retry's unit in SPEC-1 or in any restatement (summary, RECORDS-1 T-6.2.10). The `POST /v1/sessions` claim satisfies every such condition. — EVIDENCE: spec/07_session-lifecycle.md:22; spec/06_warm-pod-model.md:299
FACT: the F-6.2.27 Impl-bullet carve-out of `POST /v1/sessions` was deleted as obsolete; summary Decisions bullet "F-6.2.27 is scoped to `POST /v1/sessions/start`..." is the one home of that reason and was left as is. F-6.2.27 was NOT renumbered here (separate records directive). — EVIDENCE: summary.md:31
WATCHOUT: problem-statement.md:19 still says the lenses converged on "confining the retry to the pre-persist atomic create unit". It describes the option's framing, not the staged rule; left unedited as framing. A later round may tighten it to "the `POST /v1/sessions/start` creation unit" if it reads as the rule.


### [spec.2.fix-design-G1.1]
DECISION: SPEC-1 Edit (b) first sentence deletes the predicate "while the request has persisted no session row:" and reads "The retry applies only to the [§7.1](...) creation of `POST /v1/sessions/start`, including ..." with the rest unchanged — BECAUSE the predicate is a general condition whose extension (it admits the `POST /v1/sessions` step-4 claim, which precedes the step-5 persist) differs from the list after the colon; deleting it leaves one positive, closed statement of the unit, so no general condition remains to disagree with the list — ALTERNATIVES: naming `POST /v1/sessions` in the persisted-row group or adding a "POST /v1/sessions takes no retry" sentence (enumerates an exception to a predicate that already failed, the round-1 pattern one step smaller); narrowing the predicate to "has run setup without persisting" (same kind, still a predicate a later route can fall into); moving the unit statement to §7.1 (§7.1 and §7.2 defer to §6.2, so it would create a second home); a universal "every other request makes one attempt" (false against the §7.3 resume driver and the §5.2 slot retry).
DECISION: §6.2 **Client visibility:** stays unedited. Its `POST /v1/sessions` sentence names the code that endpoint surfaces, and §7.1 **Atomicity of session creation (steps 2–8).** already governs that failure (roll back, 503 `SESSION_CREATION_FAILED`, no retry). Once the closed list lands, the endpoint's single failure is its eventual outcome.
MISTAKE: Round 1 (spec.1.fix-design-G1.1) recorded a DECISION to replace the predicate with a closed list, but the applied text kept "while the request has persisted no session row:" and only shortened the list. That cost round 2. Fixers: diff the applied Scope sentence against the design's quoted text, not only against the finding.
WATCHOUT: Summary decision "Editing the §6.2 Client visibility: or Exhaustion: bullets" (non-goals) and decision :24 justify the bullets through "SPEC-1 defines the single attempt's failure as the exhaustion outcome". That clause covers persisted-row requests only. For `POST /v1/sessions`, §7.1 Atomicity carries the outcome. Do not answer a future "POST /v1/sessions is in neither group" finding by adding it to the persisted-row sentence, because it persists no row before its claim — EVIDENCE: spec/07_session-lifecycle.md:23, spec/06_warm-pod-model.md:302
USEFUL [spec.1.fix-design-G1.1]: its FACT on the §7.1 step-4 claim and step-5 persist order is the ground truth for this finding.


### [spec.2.review-applicability.1]
FACT: Every round-2 SPEC anchor resolves uniquely in the current tree: SPEC-2 quoted text is in the §15.1 SETUP_COMMAND_FAILED row (no RESUME_FAILED); SPEC-3(a) in the §4.7.1 ConfigureWorkspace row; SPEC-3(b) two lines at §29.2 step 23 (next line keeps "Steps 24 through 32", untouched); SPEC-3(c) "A gateway caller then fails the pod and claims a replacement" occurs once, in the **Session frame writes.** table under §28.5.3. — EVIDENCE: spec/15_external-api-surface.md:1137, spec/04_system-components.md:673, spec/29_communication-scenarios.md:286-288, spec/28_communication-channels.md:693,709
FACT: No overlap with 0086 (SPEC-5 edits §29.2 steps 14, 15, 17 only) or 0105 (edits §6.2 Client visibility "sole exception" sentence and the §7.2 visibility paragraph, not the §15.1 SETUP_COMMAND_FAILED row, §4.7 ConfigureWorkspace row, §29.2 step 23, or §28.5.3 row). — EVIDENCE: proposals/0086*/0086*.spec-changes.md:334-388; proposals/0105*/0105*.spec-changes.md:66-78,154-170
WATCHOUT: `ls proposals/NNNN*/*spec-changes.md` also matches non-spec-changes.md; filter with grep -v non-spec.


### [spec.2.review-citations.1]
FACT: The §6.2 Client visibility "sole exception" sentence currently names `starting_session → resume_pending`; approved 0105 rewrites it to "the POST /v1/sessions/{id}/start launch ... takes the starting edges of §7.2", so 0085's edge-case citation of that sentence is correct against the post-0105 spec. — EVIDENCE: spec/06_warm-pod-model.md:302; proposals/0105_*/0105_*.spec-changes.md:169
FACT: SPEC-3(c) anchor "A gateway caller then fails the pod and claims a replacement" matches spec/28_communication-channels.md:709 (§28.5.3 Intra-pod, **Session frame writes.** table); SPEC-2 anchor (no RESUME_FAILED) matches spec/15_external-api-surface.md:1137; SPEC-3(b) two-line anchor matches spec/29_communication-scenarios.md:286-287. No other spec site states a replacement claim for the ConfigureWorkspace/DemoteSDK failure. — EVIDENCE: grep "claims a replacement|a replacement is" spec/
FACT: None of 0086, 0105, 0106, 0107, 0082 stage an edit to the §15.1 SETUP_COMMAND_FAILED Description cell or the three SPEC-3 sites. — EVIDENCE: grep over proposals/*/spec-changes.md


### [spec.2.review-client-surface.1]
FACT: SPEC-2 and SPEC-3(c) anchors match the tree verbatim: §15.1 SETUP_COMMAND_FAILED row already reads "`SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback and is recovered with a fresh pod per [Section 6.2]" (no RESUME_FAILED), and the §28.5.3 row reads "A gateway caller then fails the pod and claims a replacement". EVIDENCE: spec/15_external-api-surface.md:1137, spec/28_communication-channels.md:709
FACT: docs/reference/error-catalog.md:154-155 describe only the client-side retry of SESSION_CREATION_FAILED/STARTING_FAILED, which SPEC-2 leaves intact; no client-facing docs mirror needs an edit for SPEC-2. EVIDENCE: docs/reference/error-catalog.md:155


### [spec.2.review-docs-alignment.1]
FACT: No docs/ page describes the §6.2 gateway-internal pre-attached retry loop or a replacement claim after ConfigureWorkspace+DemoteSDK failure, so SPEC-1..SPEC-3 leave no docs/ page describing superseded behavior. docs/reference/error-catalog.md:128 (SETUP_COMMAND_FAILED) already omits the "recovered with a fresh pod" clause SPEC-2 deletes; :154-155 "a retry rebinds a fresh pod" is the client retry and stays true. — EVIDENCE: grep -rn "pre-attached|fresh pod|DemoteSDK" docs/
FACT: SPEC-3(c) anchor "A gateway caller then fails the pod and claims a replacement" matches spec/28_communication-channels.md:709 verbatim; SPEC-2 anchor matches spec/15_external-api-surface.md:1137 (no RESUME_FAILED). — EVIDENCE: those lines
FACT: The edge case citing the §6.2 Client visibility "sole exception" sentence for /{id}/start is consistent with approved 0105 SPEC-2 Edit (g), which rewrites that sentence to the /start launch taking the §7.2 starting edges. — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:169


### [spec.2.review-edit-sites.1]
FACT: All SPEC-1/2/3 anchors resolve verbatim against the current tree, including the new SPEC-3(c) §28.5.3 row ("A gateway caller then fails the pod and claims a replacement") and SPEC-3(b) step 23 lines without the "Steps 24 through 32" line. EVIDENCE: spec/28_communication-channels.md:709, spec/29_communication-scenarios.md:285-288, spec/15_external-api-surface.md:1137, spec/04_system-components.md:673, spec/06_warm-pod-model.md:295-299
FACT: After SPEC-3 (a)-(c), no other spec site states a replacement claim on DemoteSDK failure; §4.7.1 says only that a deadline-outlasting DemoteSDK tears nothing down. EVIDENCE: spec/04_system-components.md:1078-1082
FACT: 0105 Edit (g) rewrites the §6.2 Client visibility "sole exception" sentence to name the /{id}/start launch and the §7.2 starting edges, so 0085's edge-case citation of that sentence holds under 0105; 0105 does not touch the lead sentence, Scope bullet, or SETUP_COMMAND_FAILED row. EVIDENCE: proposals/0105_*/...spec-changes.md:160-170
FACT: 0086 spec-changes do not edit the §15.1 SETUP_COMMAND_FAILED row or the §6.2 policy paragraph. EVIDENCE: grep of proposals/0086_*/...spec-changes.md
DEFERRED [summary.md]: Problem-statement paragraph still says the no-row assumption "holds for the §7.1 creation of POST /v1/sessions and POST /v1/sessions/start"; SPEC-1 Scope now names only POST /v1/sessions/start (POST /v1/sessions runs no setup before persisting). Reconcile in the between-loops pass.


### [spec.2.review-fresh.1]
WATCHOUT: Two round-1 fixers recorded opposite DECISIONs for the SPEC-1 Scope wording. spec.1.fix-design-G1.1 (first entry) chose a closed enumeration ("applies only to the §7.1 creation of ..."), because the predicate "while the request has persisted no session row" admits the POST /v1/sessions step-4 claim. The staged text kept that predicate and only shortened the list. The round-1 summary watch-out against wrapping the create.go claim in a retry was deleted in the same round. EVIDENCE: spec-changes.md Edit (b); spec/07_session-lifecycle.md:23 (claim at step 4, persist at step 5); summary.md:31.
FACT: SPEC-3 Edit (c) anchor "A gateway caller then fails the pod and claims a replacement" occurs once in spec/28 (line 709). The ConfigureWorkspace row at spec/04:673 sits under the heading "#### 4.7.1 Role and Gateway RPC Contract", so the §28.5.3 row's §4.7.1 citation is correct. EVIDENCE: spec/04_system-components.md:659,673
USEFUL [spec.1.fix-G2.1]: the explanation of the replacement-claim spellings meant no grep completeness check was needed.


### [spec.2.review-mechanism.1]
MISTAKE: The round-1 fixer recorded a DECISION (review-log.md:36) to replace the predicate "while the request has persisted no session row" with a closed enumeration, because the predicate admits the POST /v1/sessions step-4 claim. The staged SPEC-1 Edit (b) text (spec-changes.md:49) still opens "The retry applies only while the request has persisted no session row:", so the decided remedy was not applied. Filed as predicate drift.
FACT: 0105 Edit (g) rewrites the §6.2 Client visibility "sole exception" to "the POST /v1/sessions/{id}/start launch ... takes the starting edges of §7.2", so 0085's edge case citing that sentence for /{id}/start is consistent with 0105 — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:160-170
FACT: SPEC-3 anchors resolve verbatim in the tree: §4.7.1 ConfigureWorkspace row (spec/04_system-components.md:673), §29.2 step 23 (spec/29_communication-scenarios.md:286-287), §28.5.3 DemoteSDK-deadline row (spec/28_communication-channels.md:709). No approved proposal (0082, 0086, 0105-0107) edits these anchors or the §15.1 SETUP_COMMAND_FAILED fallback clause.


### [spec.2.review-performance.1]
FACT: 0085 stages wording-only spec edits (narrowing a retry that no code runs); it adds no etcd, Postgres, or Redis writes and no watches, so the performance lens has no write-rate math to do. EVIDENCE: proposals/0085_*/0085_*.spec-changes.md SPEC-1..SPEC-3
FACT: approved 0105 keeps a §6.2 Client visibility "sole exception" sentence, retargeted to the /{id}/start launch and the §7.2 starting edges, so the 0085 edge-case citation of that sentence still resolves after 0105 lands. EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:169


### [spec.2.review-reliability.1]
FACT: POST /v1/sessions runs only the step-3 pre-check and step-4 claim before the step-5 persist, with no setup, and a claim failure rolls back and returns 503 SESSION_CREATION_FAILED. Dropping it from the SPEC-1 Scope enumeration therefore leaves it under neither branch and creates no retry. EVIDENCE: spec/07_session-lifecycle.md:23 (Atomicity of session creation), spec/07_session-lifecycle.md:3-22 (steps 1-8)
FACT: 0105 Edit (g) rewrites the §6.2 Client visibility "sole exception" sentence to name the POST /v1/sessions/{id}/start launch and the §7.2 starting edges, so 0085's edge case that cites that sentence for /{id}/start is consistent with the post-0105 spec. EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:160-170
FACT: The SPEC-2 anchor (no RESUME_FAILED) matches the live §15.1 row, and no approved proposal (0082, 0086, 0105-0107) edits that clause. The SPEC-3(c) anchor "A gateway caller then fails the pod and claims a replacement" is live at spec/28_communication-channels.md:709. EVIDENCE: spec/15_external-api-surface.md:1137


### [spec.2.review-single-source.1]
FACT: The round-1 fixes reduced the Design section to a pointer at the SPEC-1 Scope bullet; the persisted-row rule now has one stating site (SPEC-1 Edit (b)). Edge cases cite the governing sections in one clause each and restate nothing. EVIDENCE: proposals/0085_.../0085_...spec-changes.md:8,21-24
FACT: SPEC-3 Edit (c) anchor "A gateway caller then fails the pod and claims a replacement" exists verbatim in the §28.5.3 DemoteSDK-deadline row. After SPEC-3 lands, the only remaining "claims a replacement" text in spec/ is the §7.3 resume driver step 3b, which is out of scope. EVIDENCE: spec/28_communication-channels.md:709; spec/07_session-lifecycle.md:449
FACT: After 0105 Edit (g), the §6.2 Client visibility "sole exception" sentence routes the /{id}/start launch to the §7.2 starting edges. That makes 0085 spec-changes lines 17 and 22 consistent: both are one-clause citations, not copies. EVIDENCE: proposals/0105_.../0105_...spec-changes.md:169
WATCHOUT: Summary problem statement (summary.md Summary para) still says the persisted-no-row assumption "holds for ... POST /v1/sessions". This is summary drift, which the between-loops pass reconciles. It is not a spec-loop finding.


### [spec.3.review-fresh.1]
FACT: Round-3 diff to spec-changes is one sentence: SPEC-1(b) Scope text now opens "The retry applies only to the §7.1 creation of `POST /v1/sessions/start`" (dropped "while the request has persisted no session row:"). Summary and non-spec F-6.2.27/T-6.2.10 wording were aligned to it. No other proposal site still states the dropped "persisted no session row" predicate as the scope rule; remaining "persisted-row seam" mentions describe the excluded class, consistent with the second sentence of the Scope text. — EVIDENCE: proposals/0085_*/0085_*.spec-changes.md:49
FACT: §7.3 Resume driver and §7.2 do not invoke the §6.2 pre-attached retry for /resume or /{id}/start, so the narrower "only to /start" wording contradicts no spec site; §7.2 Pre-attached visibility paragraph only defers to §6.2. — EVIDENCE: spec/07_session-lifecycle.md:226, spec/06_warm-pod-model.md:176


### [spec.3.review-mechanism.1]
DECISION: no findings on the round-3 diff — BECAUSE the only spec-changes hunk drops the "while the request has persisted no session row:" lead from SPEC-1(b), leaving "applies only to the §7.1 creation of POST /v1/sessions/start"; the persisted-row and one-attempt sentences are unchanged and no other proposal site still states the dropped predicate (grep "persisted no|pre-persist|no session row" hits only the problem statement and the T-6.2.10 gap line) — ALTERNATIVES: filing derive/replay/POST /v1/sessions as unscoped pre-persist claims; rejected because "only" excludes them and no spec site promises them a §6.2 retry (searched "fresh pod" across spec).
FACT: the MCP/adapter clause in SPEC-1(b) is backed by §15 Built-in adapter single-shot compute model ("same §15.2.1 shared session-creation service as POST /v1/sessions/start") — EVIDENCE: spec/15_external-api-surface.md:587


### [spec.4.review-applicability.1]
FACT: Every SPEC-1..3 replace-anchor matches exactly once in the current tree: §6.2 lead sentence and Scope bullet (spec/06_warm-pod-model.md:295,299), §15.1 SETUP_COMMAND_FAILED clause (spec/15_external-api-surface.md:1137), §4.7.1 ConfigureWorkspace row (spec/04_system-components.md:673), §29.2 step 23 two-line block (spec/29_communication-scenarios.md:286-287), §28.5.3 DemoteSDK-deadline row (spec/28_communication-channels.md:709). EVIDENCE: grep -c returned 1 for each.
FACT: No approved-unlanded proposal (0082 spec, 0086, 0105, 0106, 0107) stages an edit on any 0085 anchor. 0086 SPEC-5 edits §29.2 steps 14, 15, and 17 only; 0105 edit (g) replaces only the §6.2 Client visibility "sole exception" sentences, so it is disjoint from SPEC-1 (a) and (b). EVIDENCE: grep over each proposal's spec-changes.md for the anchor strings.
FACT: No test or gate under tests/, pkg/, or scripts/ pins the text SPEC-2 or SPEC-3 removes. TEST-GAPS.md T-4.7.10 quotes "a replacement is claimed", but it is an audit record and outside the spec loop. EVIDENCE: TEST-GAPS.md:991.
WATCHOUT: The ConfigureWorkspace row sits under "#### 4.7.1 Role and Gateway RPC Contract" (spec/04_system-components.md:659), not directly under §4.7. SPEC-3 (a)'s "§ 4.7 Runtime Adapter" heading names the parent section, and the row anchor is unique, so the edit still applies.


### [spec.4.review-citations.1]
FACT: Every SPEC-1..3 anchor quote matches the tree verbatim: §6.2 lead and Scope bullet (spec/06_warm-pod-model.md:295,299), §15.1 SETUP_COMMAND_FAILED row (spec/15_external-api-surface.md:1137), §4.7.1 ConfigureWorkspace row (spec/04_system-components.md:673), §29.2 step 23 (spec/29_communication-scenarios.md:286-287), §28.5.3 DemoteSDK-deadline row (spec/28_communication-channels.md:709). All cited anchors (#62, #71, #82, #1521) have headings. — EVIDENCE: as cited
FACT: The adapter implicit-session path runs the §15.2.1 shared service "as POST /v1/sessions/start" (spec/15_external-api-surface.md:587-588), and a delegated child is committed to `created` before its claim (spec/15_external-api-surface.md:657), so SPEC-1's /start-vs-persisted-row split is grounded. — EVIDENCE: as cited
FACT: 0105 SPEC-2 Edit (g) keeps a "sole exception" sentence (now the /{id}/start launch taking the §7.2 starting edges), so 0085's edge case citing that sentence survives 0105. 0086 SPEC-5 edits §29.2 steps 14, 15, 17 only, not step 23; no anchor collision with 0085 SPEC-3(b). — EVIDENCE: proposals/0105*/...spec-changes.md Edit (g); proposals/0086*/...spec-changes.md SPEC-5
WATCHOUT: `ls proposals/X*/*spec-changes.md` matches both spec-changes and non-spec-changes; filter with `grep -v non-spec` before sed -n line ranges or you read the wrong file. — EVIDENCE: proposals/0105*/


### [spec.4.review-client-surface.1]
FACT: No client-facing parallel (OpenAPI, schemas/lenny-adapter.proto, SDKs, docs/reference/error-catalog.md, client-guide, runtime-author-guide) restates the SETUP_COMMAND_FAILED "recovered with a fresh pod per Section 6.2" clause or the ConfigureWorkspace/DemoteSDK "replacement is claimed" clause, so SPEC-2 and SPEC-3 need no mirrored edit outside spec/. EVIDENCE: docs/reference/error-catalog.md:154-155 (client-retry wording only); schemas/lenny-adapter.proto:72,235 (no failure disposition)
FACT: The "MCP and adapter paths that run the same session-creation service" phrase in SPEC-1 is grounded in the built-in adapter single-shot model, which runs "the same Section 15.2.1 shared session-creation service as POST /v1/sessions/start". EVIDENCE: spec/15_external-api-surface.md:587-588
FACT: 0086 stages no edit to the §6.2 retry paragraph or to the §15.1 SETUP_COMMAND_FAILED/SESSION_CREATION_FAILED rows; 0105 Edit (f) rewrites only the STARTING_FAILED row sentence "Retryable for the same reason as SESSION_CREATION_FAILED.", which SPEC-2's anchor does not touch. EVIDENCE: proposals/0105*/0105*spec-changes.md:154


### [spec.4.review-docs-alignment.1]
FACT: docs/ never describes the §6.2 gateway-internal pre-attached retry (no "pre-attached" hit under docs/); the only fresh-pod wording is the client-retry clause in docs/reference/error-catalog.md:154-155 and the SETUP_COMMAND_FAILED row at :128, none of which claims an internal re-claim at finalize or /{id}/start — EVIDENCE: docs/reference/error-catalog.md:128,154,155
FACT: docs/api/internal.md DemoteSDK section (around :273) states no ConfigureWorkspace/DemoteSDK replacement claim, so SPEC-3 leaves no docs mirror stale — EVIDENCE: docs/api/internal.md:273-296
FACT: 0105 Edit (g) rewrites the §6.2 "sole exception" sentence to name the /{id}/start launch and the §7.2 starting edges, so 0085's edge-case row citing that sentence still resolves after 0105 — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:161-170
WATCHOUT: the glob `*spec-changes.md` also matches `*.non-spec-changes.md`; sed over the glob concatenates both and line numbers lie — use the full filename.


### [spec.4.review-edit-sites.1]
FACT: The only spec sites asserting an internal fresh-pod re-claim for pre-attached work are §6.2 policy lead and Scope bullet, §15.1 SETUP_COMMAND_FAILED row, §4.7.1 ConfigureWorkspace row, §29.2 step 23, and §28.5.3 DemoteSDK-deadline row; SPEC-1..3 cover all of them. Other "fresh pod"/"replacement" hits are resume-driver, recycle reprovision, claim-conflict, or client-retry statements (§15.1 SESSION_CREATION_FAILED "a retry rebinds a fresh pod" is a client retry). — EVIDENCE: spec/06_warm-pod-model.md:295,299; spec/15_external-api-surface.md:1133,1137; spec/04_system-components.md:673; spec/29_communication-scenarios.md:286; spec/28_communication-channels.md:709
FACT: SPEC-2 and SPEC-3 replace-from strings each match exactly once in the current tree. — EVIDENCE: grep -c on the quoted strings returned 1 each
FACT: 0086 SPEC-5 edits §29.2 steps 14, 15, 17 only, and 0086 SPEC-7 §7.5 defers setup-failure outcome to §6.2; neither touches 0085's sites. 0105 Edit (g) rewrites the §6.2 Client visibility "sole exception" sentence (consistent with 0085's edge-case pointer) and Edit (f) the STARTING_FAILED row, disjoint from SPEC-2. — EVIDENCE: proposals/0086*/0086*spec-changes.md "### SPEC-5"; proposals/0105*/0105*spec-changes.md "Edit (g) · §6.2"
WATCHOUT: docs/reference/adapter-contract.md and docs/reference/error-catalog.md also carry ConfigureWorkspace/SETUP_COMMAND_FAILED prose; any docs drift is the non-spec loop's, not this one. — EVIDENCE: docs/reference/error-catalog.md:128


### [spec.4.review-fresh.1]
FACT: Every SPEC-1/2/3 anchor matches the draft-0085 tree verbatim: §6.2 lead sentence and **Scope:** bullet, §15.1 SETUP_COMMAND_FAILED "fallback and is recovered with a fresh pod per [Section 6.2]", §4.7.1 ConfigureWorkspace row, §29.2 step 23 two-line block, §28.5.3 DemoteSDK-deadline row — EVIDENCE: spec/06_warm-pod-model.md:295,299; spec/15_external-api-surface.md:1137; spec/04_system-components.md:673; spec/29_communication-scenarios.md:286-287; spec/28_communication-channels.md:709
FACT: §7.1 itself names `POST /v1/sessions/start` as a primary creation path with roll-back-without-persist, so "the §7.1 creation of POST /v1/sessions/start" is supported; delegated children are committed to `created` before their pod is claimed (§8.8 TaskRecord table), supporting the persisted-row example — EVIDENCE: spec/07_session-lifecycle.md:23 (end of Atomicity paragraph); spec/08_recursive-delegation.md:877
FACT: 0086 SPEC-7 (a) §7.5 lead says "[Section 6.2] states the outcome of a setup failure" and 0086 deletes the §5.1 "Pod failure during setup causes pod replacement" sentence; neither conflicts with SPEC-1. 0086/0105/0106/0107 stage nothing at any 0085 anchor — EVIDENCE: proposals/0086_*/0086_*.spec-changes.md:512,520
WATCHOUT: `mintClaimStartPersist` lives in pkg/gateway/sessionserver/create_and_start.go:398, not start.go; the non-spec RECORDS-1 F-6.2.N Impl line attributes it to start.go (non-spec loop's finding, not spec) — EVIDENCE: pkg/gateway/sessionserver/create_and_start.go:398


### [spec.4.review-mechanism.1]
DECISION: no findings in round 4 — BECAUSE the only spec-changes hunk since spec-r2-prefix is the SPEC-1(b) Scope wording already checked in round 3, and every staged anchor still matches the tree verbatim: §6.2 lead and Scope (spec/06_warm-pod-model.md:295,299), §15.1 SETUP_COMMAND_FAILED clause (spec/15_external-api-surface.md:1137, 1 exact hit), §4.7.1 ConfigureWorkspace row (spec/04_system-components.md:673), §29.2 step 23 (spec/29_communication-scenarios.md:286-287), and the §28.5.3 DemoteSDK row (spec/28_communication-channels.md:709) — ALTERNATIVES: filing the unqualified "The gateway re-claims a new pod" third sentence of the §6.2 lead against the persisted-row "claims no replacement pod" sentence (rejected: the edited lead sentence scopes the retry, and the re-claim is the retry); filing that SPEC-2 removes the only contrast that keeps a deterministic setup exit out of the §6.2 loop on /start (rejected: §7.2:226 and the §15.1 row still call it non-retryable).
FACT: sibling proposals are disjoint from 0085's sites. 0086 SPEC-5 edits §29.2 steps 14, 15, and 17 (not 23), and its SPEC-7(a) §7.5 text defers setup-failure outcome to §6.2. 0086 does not touch the SETUP_COMMAND_FAILED row. 0105 edits §7.2:226 and the §6.2 "sole exception" sentence only, and impl/0105-session-timers still carries the SPEC-1 and SPEC-2 anchors verbatim (spec/06:299,303 there). 0106 and 0107 touch none of the sites. — EVIDENCE: proposals/0086_*/…spec-changes.md:334-390,512; proposals/0105_*/…spec-changes.md:66-77,160-169
FACT: 0082 is Implemented, and the §15.1 finalize precondition row now carries the overtaken-finalize 409 sentence that the spec-changes edge case cites. — EVIDENCE: spec/15_external-api-surface.md:644
WATCHOUT: §7.1 never states when POST /v1/sessions/start persists its row. The SPEC-1 relative clause "persists the row only after the claim, prepare, and launch succeed" rests on code (mintClaimStartPersist) and on §7.1 Atomicity's "roll-back-without-persist" for the primary creation path. Do not cite a §7.1 step number for it. — EVIDENCE: spec/07_session-lifecycle.md:23


### [spec.4.review-performance.1]
FACT: 0085's staged spec edits add no store write, watch, or reconcile path; they only narrow the §6.2 gateway re-claim retry to `POST /v1/sessions/start` and delete replacement-claim clauses, so at the top tier pod-claim and SandboxClaim churn can only fall (finalize and delegated-child materialization lose a spec-promised up-to-3x re-claim that the shipped code never ran). Postgres failover, Redis reset, and coordinator handoff behavior is unchanged from the shipped tree. EVIDENCE: spec/06_warm-pod-model.md:295-299; proposal spec-changes SPEC-1 Edit (b)
WATCHOUT: §8.2 `perChildRetryBudget` is a post-attached retry through the §7.3 resume flow (pod_evicted, node_lost), so SPEC-1's "delegated-child materialization takes no retry under this policy" does not contradict it. EVIDENCE: spec/08_recursive-delegation.md:303


### [spec.4.review-plan-conformance.1]
FACT: The plan assigns 0085 one queue entry and nothing else: "the §6.2 pre-attached retry scope", sequenced after 0086, plus the RECORDS-1 renumbering note. No BUILD-GAPS finding is owned by 0085 in the plan. EVIDENCE: gateway-runtime-comms-remediation.md:3303-3306, 3336 (P2b after 0085), 4344-4346
WATCHOUT: Plan step R23 (not claimed by 0085) still describes the gateway DemoteSDK fallback as "fail the pod and claim a replacement", which SPEC-3(c) removes from the §28.5.3 row. Out of this lens's scope (unclaimed step); the R23 drafter should re-read §28.5.3 after 0085 lands. EVIDENCE: gateway-runtime-comms-remediation.md:1843
DEFERRED [summary.md]: the "Records numbering note" (F-6.2.27 collision) is still present in the summary and RECORDS-1 still files F-6.2.27; the caller directive requires renumbering to the next free F-6.2.N and removing the note. Non-spec, owned by the next loop.


### [spec.4.review-reliability.1]
DECISION: no reliability findings on the round-4 staged spec edits — BECAUSE the narrowed retry runs only before store.Create on POST /v1/sessions/start, so a crash mid-loop leaves at most an unreferenced claim that the existing orphaned-SandboxClaim GC reclaims, and persisted-row seams make one attempt (no re-claim, no leaked replacement pod) — ALTERNATIVES: filing §8.2 perChildRetryBudget as a contradiction; rejected, its examples (pod_evicted, node_lost) are post-running failures governed by §7.3, not pre-attached materialization.
FACT: SPEC-2, SPEC-3(a), (b), (c) anchors still match the tree verbatim (spec/15 SETUP_COMMAND_FAILED row; spec/04 ConfigureWorkspace row; spec/29 step 23 lines 286-287; spec/28 §28.5.3 DemoteSDK-deadline row). 0086 and 0105-0107 stage no edit at those sentences; 0105 rewrites the §6.2 "sole exception" sentence to name the /{id}/start launch and §7.2 starting edges, which matches the 0085 Edge cases bullet. EVIDENCE: proposals/0105*/...spec-changes.md:169
FACT: 0086 SPEC-7 §7.5 defers setup-failure outcome to §6.2, consistent with SPEC-1. EVIDENCE: proposals/0086*/...spec-changes.md:512


### [spec.4.review-security.1]
FACT: SPEC-1 edit (a) leaves the §6.2 lead disposition sentence ("The pod is marked `failed` and released back to the pool (or terminated if unhealthy).") unconditional, so the out-of-scope single-attempt requests (finalize, delegated child) still dispose of the failed pod; §7.1 Atomicity paragraph relies on that disposition for finalize-block credential failures. EVIDENCE: spec/06_warm-pod-model.md:295, spec/07_session-lifecycle.md:22
DECISION: no security findings in round 4 — BECAUSE the staged edits only remove retry/replacement-claim clauses; the failed pod still transitions to `failed`, no control (pod reuse, fail-closed setup exit, credential release) is relaxed — ALTERNATIVES: flagging the pre-existing "released back to the pool" wording was rejected as unchanged by 0085.


### [spec.4.review-single-source.1]
FACT: The retry-scope rule has one stating site, SPEC-1 Edit (b). The Design section, Edge cases, SPEC-1 Edit (a), and SPEC-2/SPEC-3 only point at it, and spec §7.1 **Atomicity** and §7.2 **Pre-attached vs. post-attached failure visibility.** defer to §6.2 without restating scope — EVIDENCE: spec/07_session-lifecycle.md:23, spec/07_session-lifecycle.md:226
FACT: 0086 SPEC-7 Edit (a) makes §7.5 point to §6.2 for "the outcome of a setup failure" and does not touch the §15.1 SETUP_COMMAND_FAILED row, so 0085 SPEC-1/SPEC-2 stay consistent with 0086 — EVIDENCE: proposals/0086_*/0086_*.spec-changes.md:510
FACT: 0105 Edit (g) rewrites the §6.2 Client visibility "sole exception" sentence to name the /{id}/start launch and the §7.2 `starting` edges, which matches 0085's Design bullet and its /{id}/start edge case pointer — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:160-170
WATCHOUT: `0105*/*spec-changes.md` also matches non-spec-changes.md. Glob the exact `<name>.spec-changes.md` filename.

### [between-loops.reconcile.1]
CORRECTS [spec.1.fix-G2.1 DEFERRED, implementation-checklist.md]: S3 now reads "SPEC-3. Lands every SPEC-3 edit.", so it covers Edit (c) on the §28.5.3 `DemoteSDK`-deadline row and lists no sites.
CORRECTS [spec.1.fix-design-G2.1 DEFERRED, implementation-checklist.md]: same S3 rewrite as above; closed by the same edit.
CORRECTS [spec.2.review-edit-sites.1 DEFERRED, summary.md]: the Summary problem-statement paragraph now says the no-row assumption holds for the §7.1 creation of `POST /v1/sessions/start` only, matching the SPEC-1 Scope text.
CORRECTS [spec.4.review-plan-conformance.1 DEFERRED, summary.md; spec.1.fix-G1.1 OPEN]: RECORDS-1's new finding is renumbered from F-6.2.27 to F-6.2.32, the next free id (F-6.2.27 through F-6.2.31 exist in BUILD-GAPS.md). Updated in non-spec-changes.md (RECORDS-1 heading, the T-6.2.10 Dependencies bullet, the CODE-1 (c) comment) and in summary.md (Decisions, Watch out for, Non-goals, Deliverable index). The RECORDS-1 (a) insertion point moves to after the F-6.2.31 entry and before the `## §6.3 Startup Latency Analysis` heading, so the id stays in order. The summary's "Records numbering note" section is removed.
FACT: the spec.1.fix-design-G1.1 OPEN on the problem statement's "Refuted for the create paths" bullet is already closed: the bullet now says `POST /v1/sessions` "runs no setup before it persists the row". No open decision remains for the summary. — EVIDENCE: problem-statement.md "Refuted for the create paths" bullet
FACT: Deliverable index rebuilt (SPEC-1 to SPEC-3, CODE-1, TEST-1, RECORDS-1; SPEC-3 now lists spec/28). Checklist SPEC steps S1 to S3 lead in apply order; S4 to S6 Depends-on unchanged and consistent.


### [f1.out-of-scope-defects]
DECISION: item marker:spec-1 (§5.2 slot loop's missing row re-read and hard-coded `maxSlotRetries = 1`), disposition out-of-scope-stands. Wrote one entry "The §5.2 slot retry loop re-reads no session row and hard-codes its budget." at the end of summary.md `## Defects in the shipped tree that this proposal does not stage`. No decision opened, no fix staged, checklist unchanged. — EVIDENCE: pkg/gateway/sessionserver/slot_bind.go:84, pkg/gateway/sessionserver/slot_bind.go:377-416, spec/05_runtime-registry-and-pool-model.md:682-684
WATCHOUT: the gate noted that the summary's first Goal ("no other spec site asserts a fresh-pod re-claim at a persisted-row seam") overclaims while §5.2:684 still permits a slot retry on a different pod. This firing did not edit it.

### [f1.out-of-scope-defects.bind-doc-comment]
DECISION: item marker:code-1 (`Bind` doc comment's stale description of its callers), disposition out-of-scope-stands. Wrote one entry "The `Binder.Bind` doc comment names the wrong callers." at the end of summary.md `## Defects in the shipped tree that this proposal does not stage`. No decision opened, no fix staged, checklist unchanged. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:219-221, pkg/gateway/sessionserver/pod_launch.go:224

### [f1.other-proposals.0086]
DECISION: item marker:0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of (impact-row). Added a row for 0086 (Approved, approved-date 2026-10-28) to summary.md `## Impacts on other proposals`: disjoint §15.1 and §29.2 edits, every 0086 deliverable keeps its subject, the plan-rejected finalize composes with the one-attempt finalize, no new human decision, and the second to land locates its targets by quoted text. Checklist unchanged. — EVIDENCE: proposals/0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of/0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of.status.md (approved-date), 0086 spec-changes.md:248, :260, :338, :355, :372, :26; 0085 spec-changes.md:54, :88

### [f1.other-proposals.0082]
DECISION: item marker:0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for (impact-row). Rewrote the 0082 row in summary.md `## Impacts on other proposals`: status Implemented (implemented-date 2026-09-28), the handed-on §6.2 retry-loop defect resolved by SPEC-1, SPEC-2 disjoint from 0082 SPEC-1's finalize precondition row, nothing 0082 landed retired, and nothing for 0082 to do because S5 and S6 apply against the landed `start.go` and `start_pod_test.go`. Reworded the summary **Watch out for.** bullet on 0082's code steps, which said they were still to land. Checklist unchanged. — EVIDENCE: proposals/0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for/0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for.status.md:5, :13; 0082 summary.md:87; commit 79ec0e93c; 0085 spec-changes.md:49, :54
DEFERRED [/home/ec2-user/lenny/.claude/worktrees/draft-0085/proposals/0085_fix_the-6-2-pre-attached-failure-retry-loop-has/0085_fix_the-6-2-pre-attached-failure-retry-loop-has.implementation-checklist.md]: Steps S5 and S6 each end "Lands after 0082's code steps (see the summary's **Watch out for.**)." 0082 is Implemented (implemented-date 2026-09-28), so that ordering is already met. Drop the clause from both steps.


### [non-spec.1.fix-G1.1]
FACT: The R5 carves moved the CODE-1 targets. `Binder.Launch` and its doc comment are in pkg/gateway/podlifecycle/podsession/bindlaunch.go (comment :21-23, func :29). `Resume` and the `ErrNoConcurrentSlot` comment are in resume.go (:159, :184, :306-309). binder.go keeps only `Bind` (:217, :253). `writeSetupCommandError` is in pkg/gateway/sessionserver/podclaimerror.go (:270-283), and its comment ends at "Retry-After." directly above the `// spec:` tag (3f943f56f removed "The same"). EVIDENCE: grep 'fresh pod' over podsession/*.go; podclaimerror.go:279-283.
FACT: `mintClaimStartPersist` is in pkg/gateway/sessionserver/create_and_start.go:398. A `startOnPod` error rolls back through `rollbackClaim` only when `createClaimNeedsRollback` holds (:469-470). `rollbackBinding` runs only after a successful start (:495, :512). A `claimAtCreate` error has no local rollback. EVIDENCE: create_and_start.go:435, :461, :469-470.
FACT: `holdOrFailOnResumeError`, `isTransientPodClaimError`, and both `Test*SetupCommand_spec_7_3` targets of the old CODE-1(d) are gone. 3f943f56f (0092 CODE-4) deleted them. Their successors cite only "§6.2 (Pod State Machine)", which no 0085 spec edit falsifies. The fresh-pod tags left in sessionserver are podclaimerror.go:282 and podclaimerror_internal_test.go:267/:273, and CODE-1 (c) and (d) cover them. EVIDENCE: grep -rn of the four symbols in pkg and cmd returns nothing.
DECISION: The CODE-1 (b) Launch and (c) writeSetupCommandError comments point at "§6.2 pre-attached retry Scope" and name no route or unit — BECAUSE the SPEC-1 Scope bullet is the single home of which request retries, and (b) had already drifted to "§7.1 atomic creation unit", which is the POST /v1/sessions unit SPEC-1 excludes — ALTERNATIVES: writing "POST /v1/sessions/start" into both comments (two restatements that drift on the next Scope edit; both helpers are route-neutral).
WATCHOUT: Do not restore the "Do not write 'only the §7.1 atomic creation unit...'" note, and do not add a staged sentence saying Launch is reached via Bind on the resume path. The pointer comment cannot be misworded that way, and the Bind reason already has its home in the summary Decisions bullet about `Bind`. EVIDENCE: non-spec-changes.md CODE-1 (b).
WATCHOUT: Do not retarget the deleted CODE-1(d) rows to the successor resume tests, such as TestReportResumeFailureSetupCommand_spec_7_3. Their "§6.2 (Pod State Machine)" tags stay true, and CODE-1 covers only comments that the spec edits falsify. EVIDENCE: resume_setup_demotion_internal_test.go:44-45; podclaimerror_internal_test.go:360.
WATCHOUT: Do not "fix" resume.go:159/:309 or binder.go:217/:253 ("retry on a fresh pod"). CODE-1 (a) protects them on purpose because the §7.3 resume path re-claims. EVIDENCE: CODE-1 (a).
OPEN: problem-statement.md evidence bullets :38-40 still name start.go for `mintClaimStartPersist`, `bindConcurrentSlot`, and `applySlotRetryPolicy` (the slot loop is now slot_bind.go). They also name binder.go for the `Prepare`/`Launch` comments, but `Prepare` no longer carries a fresh-pod comment and `Launch` is in bindlaunch.go. The design marked :38 separate-finding, and G1 left all three untouched. A later round decides whether the dated evidence gets corrected.


### [non-spec.1.fix-G2.1]
DECISION: deleted the "Lands after 0082's code steps (see the summary's **Watch out for.**)" sentence from checklist S5 and S6 and the "ordering against proposal 0082's code steps" sentence from the non-spec Design, without retargeting — BECAUSE 0082 is Implemented, so S5 and S6 carry no ordering dependency on it and the summary's Watch out for states none — ALTERNATIVES: retarget to the summary Impacts row for 0082 (that row states no ordering either); restore an ordering bullet (it would revive a constraint that no longer holds).
FACT: 0082 status is Implemented (approved 2026-09-27, implemented 2026-09-28) — EVIDENCE: proposals/0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for/*status.md; `.claude/tools/proposal-status.mjs` prints Implemented.
OPEN: problem-statement.md still describes 0082 as "Approved, not yet applied in this tree" (Evidence item at :42, Prior art at :61, Evidence lens at :88) and says "The current tree does not yet carry 0082's conditional writes" (Premise lens at :81). The tree refutes that now that 0082 is Implemented. A later round should check those passages against the tree and correct the record. They fell outside G2's design, so G2 left them alone.


### [non-spec.1.fix-design-G1.1]
FACT: Post-R5-carve homes of the CODE-1 symbols: `Binder.Launch` doc comment in pkg/gateway/podlifecycle/podsession/bindlaunch.go (comment ends "on a fresh pod. spec: §4.4 (proposal), §6.1, §5.1."); `Bind` stays in binder.go; `Resume` doc and `ErrNoConcurrentSlot` in resume.go; `writeSetupCommandError` in pkg/gateway/sessionserver/podclaimerror.go, whose comment ends "keeps the retryable 503 fallback + Retry-After." (no trailing "The same"; 3f943f56f removed it); `mintClaimStartPersist` in pkg/gateway/sessionserver/create_and_start.go. — EVIDENCE: bindlaunch.go:21-29, resume.go:159,184,306, podclaimerror.go:277-283, create_and_start.go:398
FACT: The only fresh-pod §6.2 tags left in pkg/ are podclaimerror.go:279/282 and podclaimerror_internal_test.go:267/273; holdOrFailOnResumeError, isTransientPodClaimError and their *_spec_7_3 tests were deleted by 3f943f56f (0092 CODE-4). — EVIDENCE: grep -rn 'transient setup failure retried' pkg/
FACT: In mintClaimStartPersist a claimAtCreate error writes the envelope with no local rollback; a startOnPod error rolls back through rollbackClaim only when createClaimNeedsRollback holds; rollbackBinding runs only on the coordination-lease or store.Create failure after a successful start. — EVIDENCE: create_and_start.go:435-440, 461-474, 492, 507
DECISION: CODE-1(b) and (c) both point at "the §6.2 pre-attached retry Scope" instead of naming the route — BECAUSE the Scope bullet is the rule's one home and two code comments restating it already drifted ((b) said "§7.1 atomic creation unit", the excluded POST /v1/sessions unit) — ALTERNATIVES: changing (b) to "only the POST /v1/sessions/start creation" (still a restatement that drifts on the next Scope edit).
DECISION: Delete CODE-1(d) whole and relabel (e) as (d) — BECAUSE every (d) target was deleted from the tree — ALTERNATIVES: retargeting (d) to successor tests (they carry only "§6.2 (Pod State Machine)", which SPEC-1 does not falsify).
DECISION: Narrow the summary Watch-out bullet and the 0082 Impacts cell to S6/start_pod_test.go, and drop S5's "Lands after 0082's code steps" clause — BECAUSE after the retarget no S5 site is in a file the bullet names.
WATCHOUT: problem-statement.md:38-40 still name start.go/binder.go for mintClaimStartPersist, bindConcurrentSlot and Launch; that is evidence recorded before the carves and is not edited by G1.


### [non-spec.1.fix-design-G2.1]
DECISION: delete the 0082 ordering pointer from checklist S5 and S6 and the second sentence of the non-spec Design intro, with no retarget — BECAUSE 0082 is Implemented (summary Impacts row "Implemented (implemented-date 2026-09-28)"), so there is no ordering left to state, and the Impacts row already says S5 and S6 apply against the start.go and start_pod_test.go 0082 landed — ALTERNATIVES: retargeting to the Impacts row (rejected: it would point at a constraint that no longer exists and add a pointer that does nothing)
WATCHOUT: the summary's **Watch out for.** label is cited only by checklist:13, checklist:15 and non-spec-changes:15; once these three go, nothing in the proposal points at it. EVIDENCE: grep "Watch out for" over the proposal files


### [non-spec.1.review-applicability.1]
FACT: CODE-1 was staged against the pre-0092 tree. Commit 3f943f56f (2026-10-13, resume driver) deleted holdOrFailOnResumeError and isTransientPodClaimError and the "The same" sentence in the writeSetupCommandError comment; the successor tests (TestResumeFailureReasonSetupCommand_spec_7_3, TestReportResumeFailureSetupCommand_spec_7_3) carry only "§6.2 (Pod State Machine)" with no fresh-pod clause — EVIDENCE: pkg/gateway/sessionserver/podclaimerror_internal_test.go:352-361, resume_setup_demotion_internal_test.go:44-55
FACT: R5 carves moved the CODE-1 sites: Binder.Launch is in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-29 (not binder.go); writeSetupCommandError is in pkg/gateway/sessionserver/podclaimerror.go:267-283 (not start.go); mintClaimStartPersist is in create_and_start.go:398 (not start.go); Binder.Resume and the ErrNoConcurrentSlot comment are in resume.go — EVIDENCE: git log -S a273d5a7c, 083deba8b
WATCHOUT: "§7.1 atomic creation unit" in spec/07 line 23 is the POST /v1/sessions steps 2–8 unit (pre-check, claim, persist), the unit SPEC-1 excludes; any comment or record that names the retry's scope must say POST /v1/sessions/start — EVIDENCE: spec/07_session-lifecycle.md:23
FACT: Spec anchors for SPEC-1..3 all resolve uniquely in the current tree; TEST-1 anchors (podBindBinder takes client.Client, sync/atomic and lennyv1 imported, claim goes through Binder.Client → podclaim.CreateClaim cl.Create) hold — EVIDENCE: start_pod_test.go:18,35,213,1099; podclaim/claimer.go:469
FACT: F-6.2.32 is free; last §6.2 entry is F-6.2.31 at BUILD-GAPS.md:5719, followed by "## §6.3 Startup Latency Analysis" at :5728.


### [non-spec.1.review-citations.1]
FACT: R5 carved start.go and binder.go on 2026-09-28 (083deba8b, a273d5a7c). Binder.Launch and its doc comment live in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-29; writeSetupCommandError lives in pkg/gateway/sessionserver/podclaimerror.go:268-283; mintClaimStartPersist lives in pkg/gateway/sessionserver/create_and_start.go:398. start.go carries no "fresh pod" comment at all. — EVIDENCE: grep -rn "func (b \*Binder) Launch\|func (s \*Server) writeSetupCommandError\|mintClaimStartPersist" pkg/
FACT: holdOrFailOnResumeError, isTransientPodClaimError, TestIsTransientPodClaimErrorSetupCommand_spec_7_3, and TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3 do not exist anywhere in pkg/ (resume path rewritten in 3f943f56f). The present resume tests (TestResumeFailureReasonSetupCommand_spec_7_3, TestReportResumeFailureSetupCommand_spec_7_3) tag "§6.2 (Pod State Machine)" with no fresh-pod claim. — EVIDENCE: pkg/gateway/sessionserver/podclaimerror_internal_test.go:360, resume_setup_demotion_internal_test.go:44-45
WATCHOUT: the writeSetupCommandError "Replace" block in CODE-1(c) ends "Retry-After. The same", but the real comment ends "keeps the retryable 503 fallback + Retry-After." followed by the spec tag line; there is no "The same" continuation. — EVIDENCE: pkg/gateway/sessionserver/podclaimerror.go:279-282
FACT: CODE-1(e) quotes match podclaimerror_internal_test.go:267 and :273 exactly; TEST-1 fixture claims (sync/atomic import line 18, podBindBinder(c client.Client, ...) line 213, claim-sbx-1, runFinalizeMaterializationFailureCase line 1099) hold. BUILD-GAPS F-6.2.31 is the last §6.2 entry before "## §6.3" (line 5728), so F-6.2.32 is free. — EVIDENCE: BUILD-GAPS.md:5719-5728


### [non-spec.1.review-client-surface.1]
FACT: 0085 changes no client-facing contract. No docs/, OpenAPI (pkg/gateway/externalapi/openapi/openapi.json), SDK, or schema text claims an internal §6.2 fresh-pod retry at finalize, /{id}/start, or delegation; docs/reference/error-catalog.md:154-155 describe a client retry only — EVIDENCE: lookup over docs/schemas/sdks for "fresh pod", "pre-attached", "automatic retr".
FACT: The CODE-1 targets moved after 3f943f56f/083deba8b: `Binder.Launch` doc comment is pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23 (not binder.go); `writeSetupCommandError` is pkg/gateway/sessionserver/podclaimerror.go:268-283 (not start.go) and its comment has no "The same" continuation; `mintClaimStartPersist` is pkg/gateway/sessionserver/create_and_start.go:398 (not start.go) — EVIDENCE: those lines.
USEFUL [spec.1.fix-design-G1.1]: the WATCHOUT that `holdOrFailOnResumeError` and `isTransientPodClaimError` no longer exist is correct; the two CODE-1 (d) test functions do not exist either, and resume_setup_demotion_internal_test.go:44-45 carries no "retried on a fresh pod" clause, so all four CODE-1 (d) rows locate nothing.


### [non-spec.1.review-docs-alignment.1]
FACT: docs/ never describes the §6.2 gateway-internal pre-attached retry (no "pre-attached", no "3 total attempts", no finalize/start internal-retry prose), so narrowing it to POST /v1/sessions/start leaves no docs page stale. EVIDENCE: lookup exact over docs for "pre-attached", "fresh pod", "finalize...retr", "sessions/start...retr" returned only client-retry rows in docs/reference/error-catalog.md:128,154,155 and resume-driver prose.
FACT: docs/reference/error-catalog.md:128 SETUP_COMMAND_FAILED row already omits the "recovered with a fresh pod per §6.2" clause SPEC-2 deletes from §15.1, so it is consistent after SPEC-2. EVIDENCE: docs/reference/error-catalog.md:128
FACT: no docs page states the "DemoteSDK also fails ... replacement is claimed" clause SPEC-3 deletes; docs/api/internal.md DemoteSDK section and adapter-contract.md:66 describe the RPC only. EVIDENCE: docs/api/internal.md:273-293, docs/reference/adapter-contract.md:66
DECISION: no docs-alignment findings — BECAUSE 0085 changes no production behavior and the spec clauses it edits have no docs mirror — ALTERNATIVES: flagging error-catalog STARTING_FAILED "a retry rebinds a fresh pod" (155) rejected; it is client-retry prose, pre-existing, and already listed in the summary's unstaged-defects (exclusive-pool /{id}/start stale binding).


### [non-spec.1.review-edit-sites.1]
FACT: CODE-1 file attribution is stale. `Binder.Launch` doc comment lives in pkg/gateway/podlifecycle/podsession/bindlaunch.go:14-23 (not binder.go); `writeSetupCommandError` lives in pkg/gateway/sessionserver/podclaimerror.go:268-282 (not start.go), and its block ends "keeps the retryable 503 fallback + Retry-After." with no "The same" (0092 cut that sentence). The `Resume` doc comment and the `ErrNoConcurrentSlot` comment are in podsession/resume.go:155-159 and :306-310. EVIDENCE: grep of pkg/ for "returned so the gateway retries", "§6.2 recovers on a fresh pod".
FACT: `holdOrFailOnResumeError`, `isTransientPodClaimError`, `TestIsTransientPodClaimErrorSetupCommand_spec_7_3`, `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` no longer exist (0092 CODE-4). CODE-1 (d) locates nothing; after CODE-1 (b)/(c) are re-pointed, start.go has no CODE-1 site at all. EVIDENCE: grep -rn of pkg/ returns nothing; proposals/0092_*/0092_*.summary.md:174.
FACT: `mintClaimStartPersist` is in pkg/gateway/sessionserver/create_and_start.go:398, not start.go (F-6.2.32 Impl text). claimAtCreate is pod_claim.go:105, startOnPod pod_launch.go:108, rollback* in bind_rollback.go.
FACT: No docs/, schemas/, or charts/ surface states the §6.2 gateway-internal pre-attached retry; error-catalog "a retry rebinds a fresh pod" is a client retry and stays true. EVIDENCE: grep -i "pre-attached" docs/ charts/ schemas/ returns nothing; docs/reference/error-catalog.md:154-155.
USEFUL [spec.1.fix-design-G1.1]: its WATCHOUT on the deleted resume helpers was correct and pointed straight at the CODE-1 (d) defect.


### [non-spec.1.review-fresh.1]
FACT: R5 carves moved CODE-1 targets. `Binder.Launch` and its doc comment live in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-29 (binder.go keeps only `Bind`). `writeSetupCommandError` lives in pkg/gateway/sessionserver/podclaimerror.go:270-285, and its current third line is "// keeps the retryable 503 fallback + Retry-After." with no trailing "The same". `mintClaimStartPersist` is in create_and_start.go:398. EVIDENCE: git log -S (083deba8b, a273d5a7c)
FACT: `holdOrFailOnResumeError`, `isTransientPodClaimError`, `TestIsTransientPodClaimErrorSetupCommand_spec_7_3`, and `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` have no occurrence in pkg/. They were removed by 3f943f56f, so CODE-1 (d) has no live target. EVIDENCE: grep -rn over pkg/
USEFUL [review-log line 40, line 275]: both WATCHOUTs (CODE-1 (d) dead, mintClaimStartPersist file) still hold in the non-spec text and are filed this round.
FACT: After CODE-1 is retargeted, the remaining §6.2 "fresh pod" comments the spec edits falsify are podclaimerror.go:279-282 and podclaimerror_internal_test.go:267,273. docs/, schemas/, and charts/ carry no pre-attached retry claim. EVIDENCE: grep "fresh pod" over pkg/ and docs/


### [non-spec.1.review-mechanism.1]
FACT: `Binder.Launch` and its doc comment live in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-23, not binder.go. `writeSetupCommandError` lives in pkg/gateway/sessionserver/podclaimerror.go:268-283, not start.go, and its third quoted line ends "Retry-After." with no "The same" continuation. `mintClaimStartPersist` lives in pkg/gateway/sessionserver/create_and_start.go:398. `Binder.Resume` doc comment is in resume.go:159. — EVIDENCE: grep of pkg/
FACT: `holdOrFailOnResumeError`, `isTransientPodClaimError`, `TestIsTransientPodClaimErrorSetupCommand_spec_7_3`, and `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` exist nowhere in pkg/ (0092 removed them); CODE-1 (d) targets nothing. The only remaining "§6.2 ... fresh pod" comment sites are podclaimerror.go:279,282 and podclaimerror_internal_test.go:267,273. — EVIDENCE: grep -rn over pkg/
WATCHOUT: "§7.1 atomic creation unit" names §7.1 **Atomicity of session creation (steps 2–8).**, which is the POST /v1/sessions pre-check/claim/persist unit SPEC-1 excludes. Any code comment or record saying the §6.2 retry applies inside "the §7.1 atomic creation unit" without naming POST /v1/sessions/start reintroduces the rejected predicate. — EVIDENCE: spec/07_session-lifecycle.md:22
FACT: The MCP and adapter create-and-start paths reuse handleCreateAndStart (service.go:247), so a loop in mintClaimStartPersist covers every SPEC-1 Scope route. — EVIDENCE: pkg/gateway/sessionserver/service.go:218-247


### [non-spec.1.review-performance.1]
DECISION: performance lens returns no findings for 0085 — BECAUSE the staged work is comments (CODE-1), one test-only client wrapper (TEST-1), and records (RECORDS-1); no production code token, store write, watch, or reconcile path changes, and SPEC-1 only narrows where the existing §6.2 retry runs (fewer SandboxClaim creates at finalize, none added) — ALTERNATIVES: quantifying the future F-6.2.32 loop's claim amplification was rejected as out of scope, since the 2-retry budget is pre-existing §6.2 text 0085 does not change.
FACT: F-6.2.31 is the last §6.2 entry in BUILD-GAPS.md and is followed directly by the `## §6.3 Startup Latency Analysis` heading, so F-6.2.32 is free and RECORDS-1(a)'s insertion anchor resolves — EVIDENCE: BUILD-GAPS.md:5719, BUILD-GAPS.md:5728
FACT: start_pod_test.go already imports sync/atomic and podBindBinder takes a client.Client, so TEST-1's counter wrapper composes — EVIDENCE: pkg/gateway/sessionserver/start_pod_test.go:18, :213, :1099


### [non-spec.1.review-plan-conformance.1]
FACT: The plan's only entry for 0085 is the "Queue after P4b" bullet; it assigns two deliverables (the §6.2 pre-attached retry scope, and RECORDS-1's finding renumbered off F-6.2.27). Both are staged (SPEC-1; F-6.2.32, which is free). No other plan line names 0085's content — EVIDENCE: gateway-runtime-comms-remediation.md:3303-3306; BUILD-GAPS.md:5690-5728
WATCHOUT: The plan says 0085 follows 0086 "because both edit the §6.2 setup-failure wording", but 0086 edits §6.1 (demotion timing) and §7.5, not §6.2; this is sequencing rationale, not a deliverable — EVIDENCE: proposals/0086_*/0086_*.spec-changes.md:158, :512
DECISION: plan-conformance lens returns no findings — BECAUSE every plan-assigned deliverable is staged and the summary's numbering note is already removed — ALTERNATIVES: filing the stale §6.2 overlap rationale was rejected as a plan-wording issue with no repository consequence.


### [non-spec.1.review-reliability.1]
FACT: CODE-1 was drafted 2026-09-27 (c26428dcc), before the R5 carves of binder.go (a273d5a7c) and start.go (083deba8b) and before the resume-driver rewrite (3f943f56f). `Binder.Launch` doc comment now lives in pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23; `Resume` doc comment in resume.go:159; `writeSetupCommandError` in pkg/gateway/sessionserver/podclaimerror.go:268-283 (its comment ends "Retry-After." with no "The same" continuation); `mintClaimStartPersist` in create_and_start.go:398. — EVIDENCE: grep -rn "§6.2 (transient" pkg/ returns only podclaimerror.go:282 and podclaimerror_internal_test.go:267
FACT: `holdOrFailOnResumeError`, `isTransientPodClaimError`, `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3`, and `TestIsTransientPodClaimErrorSetupCommand_spec_7_3` no longer exist (removed by 3f943f56f); resume_setup_demotion_internal_test.go carries no "§6.2 (transient" clause. — EVIDENCE: git log -S holdOrFailOnResumeError
WATCHOUT: any CODE-1 rewrite must re-grep the live tree for "fresh pod" comments; the only §6.2-falsified sites left are bindlaunch.go:21-23, podclaimerror.go:278-282, podclaimerror_internal_test.go:267 and :273.
FACT: TEST-1's counter premise holds: Binder.claimPod builds podclaim.Claimer{Client: b.Client} (claimconnect.go:46-48) whose Claim calls cl.Create (podclaim/claimer.go:469), and ReclaimClaimed only deletes the claim (reclaim.go:132,162), so sbx-1 stays claimable in envtest. — EVIDENCE: pkg/gateway/podlifecycle/podsession/claimconnect.go:45-58


### [non-spec.1.review-security.1]
FACT: Security lens finds no control regression. 0085 changes only comments, tests, and records. The failed pod still goes to `failed`, and the F-6.2.32 gap text releases each attempt's pod and credential lease before the next claim. EVIDENCE: proposals/0085_.../0085_...non-spec-changes.md:94
FACT: CODE-1 sites drifted. The `Binder.Launch` doc comment is in pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, not binder.go. `writeSetupCommandError` is in pkg/gateway/sessionserver/podclaimerror.go:270-283, not start.go, and its comment ends "Retry-After.\n// spec:" rather than "Retry-After. The same". `holdOrFailOnResumeError`, `isTransientPodClaimError`, `TestIsTransientPodClaimErrorSetupCommand_spec_7_3`, and `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` no longer exist anywhere in pkg/. Commit 3f943f56f (resume driver) removed them. EVIDENCE: grep -rn holdOrFailOnResumeError pkg/ returns nothing
WATCHOUT: Re-grep every CODE-1 anchor against the tree before trusting it. The proposal was drafted before the resume-driver rewrite. EVIDENCE: git log -S holdOrFailOnResumeError


### [non-spec.1.review-single-source.1]
FACT: The Binder.Launch doc comment lives in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-23, not binder.go; Resume and the ErrNoConcurrentSlot comment live in resume.go (159, 306) — EVIDENCE: bindlaunch.go:22 "via failPhase and is returned so the gateway retries"
FACT: writeSetupCommandError and its §6.2 tag live in pkg/gateway/sessionserver/podclaimerror.go:279-282, not start.go; its comment has no "The same" continuation — EVIDENCE: podclaimerror.go:280 "keeps the retryable 503 fallback + Retry-After."
FACT: holdOrFailOnResumeError, isTransientPodClaimError, TestIsTransientPodClaimErrorSetupCommand_spec_7_3, and TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3 no longer exist (removed by 3f943f56f, the resume-driver commit). The only §6.2 "fresh pod" tags left are podclaimerror.go:279-282 and podclaimerror_internal_test.go:267,273 — EVIDENCE: grep "retried on a fresh pod" over pkg/gateway
WATCHOUT: "the §7.1 atomic creation unit" by itself names the POST /v1/sessions steps 2-8 unit (spec/07_session-lifecycle.md:22). After SPEC-1 narrowed scope to POST /v1/sessions/start, any non-spec text that says "the §7.1 atomic creation unit" without the route restates the scope wrongly — EVIDENCE: non-spec-changes.md:39
WATCHOUT: 0082 is Implemented (status file); checklist S5/S6 "Lands after 0082's code steps" and the non-spec Design pointer to the summary's ordering are stale since the summary watch-out was rewritten.


### [non-spec.1.review-test-coverage.1]
FACT: TEST-1 mechanics hold. The binder claims through b.Client (claimconnect.go:47, fallbackclaim.go:117), podBindClient is envtest (start_pod_test.go:93-95), and runFinalizeMaterializationFailureCase seeds only sbx-1 (start_pod_test.go:1099-1120). `sync/atomic`, `client`, and `lennyv1` are already imported. EVIDENCE: pkg/gateway/sessionserver/start_pod_test.go:18,30,35
FACT: MaterializeDelegatedChild calls the same claimAtCreate(..., claimRouteStart) + startOnPod pair that F-6.2.32 tells the builder to wrap in a loop inside mintClaimStartPersist. A shared retry helper would reach the delegated-child seam silently. EVIDENCE: pkg/gateway/sessionserver/delegated_child_materialize.go:110,141
FACT: mintClaimStartPersist lives in create_and_start.go:398, not start.go. writeSetupCommandError lives in podclaimerror.go:268-283, not start.go. holdOrFailOnResumeError, isTransientPodClaimError, and the two `_spec_7_3` tests that CODE-1 (d) names do not exist (0092 deleted them). These are edit-site and attribution findings for other lenses. EVIDENCE: grep of pkg/
WATCHOUT: TestMaterializeDelegatedChildAssignmentRaceFailsClosed_spec_8_3 (delegated_child_materialize_test.go:480) fails after the claim and seeds only sbx-1, so the claimCreateCounter drops in there with no fixture change.


### [non-spec.2.review-applicability.1]
FACT: CODE-1 post-carve anchors all resolve uniquely: Launch doc comment old block at pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23; writeSetupCommandError comment at pkg/gateway/sessionserver/podclaimerror.go:278-282; test annotation at pkg/gateway/sessionserver/podclaimerror_internal_test.go:266-273. EVIDENCE: those lines.
FACT: holdOrFailOnResumeError, isTransientPodClaimError, and their _spec_7_3 tests no longer exist anywhere in pkg/, so dropping old CODE-1(d) loses no edit site. EVIDENCE: grep -rn over pkg returns nothing.
FACT: Resume doc comment is resume.go:154-159 and the ErrNoConcurrentSlot comment is the inline one at resume.go:306-310; Bind's only production caller is pod_launch.go:224. EVIDENCE: those lines.
DECISION: no findings this round — BECAUSE every hunk's new file names, quoted text, and the F-6.2.32 Impl rollback claim (create_and_start.go:398, rollbackClaim gated by createClaimNeedsRollback) match the tree, and no stale binder.go/start.go/0082-ordering reference remains in the proposal outside the problem statement (a lead) and the summary's pre-existing Bind-callers defect entry, which still cites binder.go correctly.


### [non-spec.2.review-citations.1]
FACT: After the podsession/sessionserver carve, the CODE-1 sites are: Launch doc comment in pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, Bind comments in binder.go:217 and :253, Resume doc and ErrNoConcurrentSlot comments in resume.go:159 and :306-310, writeSetupCommandError in pkg/gateway/sessionserver/podclaimerror.go:268-282, test annotation at podclaimerror_internal_test.go:267-273. All quoted Replace texts match the tree verbatim. — EVIDENCE: grep "fresh pod" over both packages
FACT: The old /resume tag sites (holdOrFailOnResumeError, isTransientPodClaimError, TestHoldOrFail*/TestIsTransient*) no longer exist anywhere in pkg/, so dropping old CODE-1(d) was correct. — EVIDENCE: grep -rn returns nothing
FACT: mintClaimStartPersist is pkg/gateway/sessionserver/create_and_start.go:398; startOnPod error -> rollbackClaim gated on createClaimNeedsRollback; rollbackBinding is used only after the coordination-lease and store.Create failures, matching the rewritten F-6.2.32 Impl bullet.
WATCHOUT: problem-statement.md lines 36-40 still name pre-carve files (start.go, binder.go); it is a lead/history record, not a staging site. — EVIDENCE: problem-statement.md:38-40


### [non-spec.2.review-client-surface.1]
FACT: The r1->r2 diff only retargets CODE-1 to post-carve files (bindlaunch.go, podclaimerror.go, podclaimerror_internal_test.go), drops the /resume-helper tag table, removes the 0082 ordering pointers, and fixes the F-6.2.32 Impl file. No REST/OpenAPI/MCP/SDK/schema/CRD/docs surface is touched. CODE-1 (b), (c), (d) anchors match the tree verbatim — EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, pkg/gateway/sessionserver/podclaimerror.go:278-282, pkg/gateway/sessionserver/podclaimerror_internal_test.go:266-273
FACT: No other "§6.2 (transient setup failure retried on a fresh pod)" tag remains in pkg/ beyond the sites CODE-1 targets, so dropping the old (d) table leaves no stale site — EVIDENCE: grep of pkg/ for "§6.2 (transient"


### [non-spec.2.review-edit-sites.1]
FACT: After the podsession carve, Launch's doc comment lives in bindlaunch.go:15-23, Prepare's in bindprepare.go:53-62 (no fresh-pod retry wording), Bind's in binder.go:212-224, and Resume/ErrNoConcurrentSlot in resume.go. writeSetupCommandError is in sessionserver/podclaimerror.go:268-282. — EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:22, pkg/gateway/sessionserver/podclaimerror.go:279-282
FACT: The only remaining "§6.2 (transient setup failure retried on a fresh pod)" / "§6.2 recovers" sites in Go are podclaimerror.go:279,282 and podclaimerror_internal_test.go:267,273, which matches the r2 CODE-1 (c) and (d); the dropped /resume-helper row (old (d)) has no tree site left. — EVIDENCE: grep -rn over pkg cmd tests
WATCHOUT: errorclassify.go:458 ("the failing unit rebinds a fresh pod") describes client-retry classification of SESSION_CREATION_FAILED/STARTING_FAILED; it is not falsified by SPEC-1 and is not an edit site. — EVIDENCE: pkg/gateway/externalapi/errorclassify/errorclassify.go:455-462


### [non-spec.2.review-fresh.1]
FACT: After the carve, the Launch doc comment lives in pkg/gateway/podlifecycle/podsession/bindlaunch.go:15-23, writeSetupCommandError in pkg/gateway/sessionserver/podclaimerror.go:268-282, Bind in binder.go:211-231, and the Resume doc comment plus the ErrNoConcurrentSlot handling in resume.go:159,306-311. CODE-1's quoted old blocks match the tree verbatim. EVIDENCE: grep "retried on a fresh pod" in pkg/ returns only podclaimerror.go:282 and podclaimerror_internal_test.go:267
FACT: The tag sites that the former CODE-1(d) targeted (holdOrFailOnResumeError, isTransientPodClaimError, and the resume_setup_demotion test) no longer carry a §6.2 fresh-pod clause, so removing (d) was correct. EVIDENCE: grep -rn "§6.2 (transient" pkg/
FACT: mintClaimStartPersist is at create_and_start.go:398, and rollbackClaim runs only on a startOnPod error that is gated by createClaimNeedsRollback (offset +64..+77), which matches the revised F-6.2.32 Impl bullet.


### [non-spec.2.review-mechanism.1]
FACT: After the podsession carve, Launch's doc comment lives in bindlaunch.go (lines 15-23), Resume and the ErrNoConcurrentSlot comment in resume.go (159, 306), Bind stays in binder.go (210-221, 253), and writeSetupCommandError in sessionserver/podclaimerror.go (267-282). CODE-1(b) and (c) anchors match the tree verbatim. EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23; pkg/gateway/sessionserver/podclaimerror.go:278-282
FACT: The removed CODE-1(d) table sites (holdOrFailOnResumeError, isTransientPodClaimError, the two resume-test annotations) no longer carry a "transient setup failure retried on a fresh pod" tag; grep finds that phrase only at podclaimerror.go:282 and podclaimerror_internal_test.go:267, both still staged. EVIDENCE: grep -rn "transient setup failure" pkg
FACT: mintClaimStartPersist is at create_and_start.go:398; on startOnPod error it calls rollbackClaim (470); rollbackBinding (495, 512) is the post-success path, matching the revised F-6.2.32 Impl bullet.


### [non-spec.2.review-reliability.1]
FACT: The post-carve CODE-1 targets resolve. The Launch doc comment is at pkg/gateway/podlifecycle/podsession/bindlaunch.go:14-23, the writeSetupCommandError comment at pkg/gateway/sessionserver/podclaimerror.go:268-282, and the test annotation at podclaimerror_internal_test.go:266-273. The Bind comments are at binder.go:217 and 253, and the Resume and ErrNoConcurrentSlot comments at resume.go:159 and 306-310. Every quoted "Replace" anchor matches the tree verbatim. EVIDENCE: the files and lines above
FACT: mintClaimStartPersist is at create_and_start.go:398. On a startOnPod error it calls only rollbackClaim, gated by createClaimNeedsRollback (lines 469-470). rollbackBinding runs only on a coordination-lease failure or a store.Create failure (lines 495 and 512), so the F-6.2.32 Impl bullet is accurate. EVIDENCE: pkg/gateway/sessionserver/create_and_start.go:463-512
FACT: The earlier CODE-1(d) sites (holdOrFailOnResumeError, isTransientPodClaimError, and their test annotations) no longer exist in pkg/. No other §6.2 "transient setup failure retried on a fresh pod" tag remains outside the CODE-1 sites. EVIDENCE: grep of pkg cmd sdks tests


### [non-spec.2.review-security.1]
FACT: After the R5 carve, the CODE-1 anchors resolve as staged: the Launch doc comment is at bindlaunch.go:21-23, writeSetupCommandError's comment is at podclaimerror.go:279-282, and the test annotation is at podclaimerror_internal_test.go:267,273. The only remaining "transient setup failure retried on a fresh pod" sites under pkg/ are those last two files, so dropping the old (d) /resume table leaves no stale tag. EVIDENCE: grep -rn over pkg/
DECISION: no security findings in round 2. BECAUSE the diff changes only comment text, file paths, and records, and it touches no RBAC, no admission or network control, no reuse counter, and no bound. ALTERNATIVES: none.


### [non-spec.2.review-single-source.1]
FACT: After the podsession carve, Launch's doc comment is in bindlaunch.go:21-23, Bind's in binder.go:217/253, Resume's in resume.go:159, and the ErrNoConcurrentSlot inline comment in resume.go:306-311. writeSetupCommandError is in sessionserver/podclaimerror.go:277-282. No "retried on a fresh pod" tag remains in start.go, so the old CODE-1(d) /resume table was correctly dropped. EVIDENCE: grep "fresh pod" pkg/gateway
FACT: mintClaimStartPersist is in create_and_start.go; on a startOnPod error it calls rollbackClaim only when createClaimNeedsRollback holds (create_and_start.go:469-470). EVIDENCE: create_and_start.go:461-474
DECISION: no single-source findings on the r1→r2 diff. BECAUSE the new Launch and writeSetupCommandError comments cite the §6.2 Scope and do not restate it, and the 0082 ordering references now appear only once, in summary Watch-out plus the impacts row, which agree (S6 only). ALTERNATIVES: filing summary :25 and :83 as copies of "Bind's only production caller is the resume rebuild". Rejected because that is one fact used for two different purposes, and neither site states a rule.


### [non-spec.3.review-applicability.1]
FACT: implement-proposal computes findingIds in its Plan phase, before any step runs, so the F-6.2.32 entry that S4 creates is never in the close list; the summary's "must stay OPEN" watch-out is consistent with the pipeline. EVIDENCE: .claude/workflows/implement-proposal.js:259-262 (findingIds from plan), :497-517 (close uses plan.findingIds)
FACT: All CODE-1 (b)/(c)/(d), TEST-1, and RECORDS-1 anchors still resolve uniquely in the draft-0085 tree: bindlaunch.go:21-23, podclaimerror.go:278-282, podclaimerror_internal_test.go:266-273, start_pod_test.go:1043-1057/1099/213, BUILD-GAPS.md F-6.2.31 at :5719 then "## §6.3" at :5728, TEST-GAPS.md T-6.2.10 at :1788 with exactly five bullets. EVIDENCE: those lines
FACT: 0086 SPEC-7(a) staged §7.5 text says "[Section 6.2] states the outcome of a setup failure", which defers to the §6.2 home 0085 SPEC-1 scopes; no conflict. EVIDENCE: proposals/0086_*/0086_*.spec-changes.md:512
DECISION: no applicability findings in round 3 — BECAUSE lanes are one per step and valid (spec, docs, code, test), spec steps lead, every Depends-on names an earlier step, S5's F-6.2.32 citation follows S4, and every deliverable appears in exactly one step — ALTERNATIVES: flagging S5's extra dependency on S2 (harmless, not a forward reference).
USEFUL [non-spec.2.review-applicability.1]: its anchor line map was still accurate and saved re-deriving the carve locations.


### [non-spec.3.review-citations.1]
FACT: Every CODE-1 anchor resolves verbatim at HEAD 0e72890c7: Launch doc comment in bindlaunch.go:21-23, writeSetupCommandError comment in podclaimerror.go:278-282 (anchor ends at "Retry-After." then the spec: line), TestWritePodClaimErrorSetupCommandFailed_spec_7_3 annotation in podclaimerror_internal_test.go:266-273. No other Go comment outside these cites "§6.2 (transient setup failure..." — EVIDENCE: grep of pkg/ for "§6.2 (transient" returns only podclaimerror.go:282 and podclaimerror_internal_test.go:267.
FACT: Bind's only production caller is pod_launch.go:224 (resume rebuild via runWithQueue); the Bind doc comment's "(the §4.7 combined create-and-start path and the test harness)" sits at binder.go:219-221; Resume doc and ErrNoConcurrentSlot comment are in resume.go:159 and :306-310 — EVIDENCE: grep "podBinder.Bind(" in pkg/gateway.
FACT: TEST-1's discriminating counter works in the fixture: podBindIdleSandbox seeds no corev1.Pod, so AdmitTenantPin admits on NotFound (tenantpin.go:85-88), and the gateway never writes Sandbox.Status.Phase (only claim phases in bindingstate.go), so a regressing re-claim would call CreateClaim on sbx-1 again — EVIDENCE: start_pod_test.go:183-191, podclaim/claimer.go:189,214.
FACT: F-6.2.32 Impl bullet matches create_and_start.go:398-470 (claimAtCreate error writes without rollback; startOnPod error -> rollbackClaim iff createClaimNeedsRollback). F-6.2.32 is the next free number (F-6.2.31 at BUILD-GAPS.md:5719, §6.3 heading at :5728). T-6.2.10 has exactly five bullets (TEST-GAPS.md:1789-1793).
FACT: 0086/0105/0106/0107 spec-changes do not touch the §6.2 Scope bullet, the §15.1 SETUP_COMMAND_FAILED fallback clause, the §4.7.1 ConfigureWorkspace row, §29.2 step 23, or the §28.5.3 DemoteSDK-deadline row; impl/0105-session-timers has no diff on the CODE-1/TEST-1 files.


### [non-spec.3.review-client-surface.1]
FACT: The only client-facing surface 0085 changes is the §15.1 `SETUP_COMMAND_FAILED` row (SPEC-2). Its parallels already agree: docs/reference/error-catalog.md:128 states a client retry on the fallback codes and claims no internal fresh-pod recovery, and pkg/gateway/externalapi/openapi/openapi.json and sdks/ carry no "fresh pod" or `SETUP_COMMAND_FAILED` text — EVIDENCE: grep of openapi.json and sdks/ returns nothing.
FACT: The built-in adapter single-shot path (OpenAI completions, Open Responses) and the MCP create-and-start path reach `mintClaimStartPersist` through `CreateAndStartService`, which replays `handleCreateAndStart` against a recorder, so F-6.2.32's single loop site covers SPEC-1's "MCP and adapter paths" — EVIDENCE: pkg/gateway/sessionserver/service.go:233-246, pkg/gateway/sessionserver/create_and_start.go:88.


### [non-spec.3.review-docs-alignment.1]
FACT: docs/ never describes the §6.2 gateway-internal pre-attached retry, a fresh-pod re-claim at finalize, /{id}/start, or delegated-child materialization, or a replacement claim after a DemoteSDK failure. The only "fresh pod" claims are the error-catalog SESSION_CREATION_FAILED and STARTING_FAILED rows, which describe a client retry rebinding a pod and stay true under SPEC-1/2/3. EVIDENCE: docs/reference/error-catalog.md:128,154,155; docs/reference/adapter-contract.md:65-66; docs/api/internal.md:273-283
FACT: The accepted edge case (terminal writer overtakes the single finalize attempt) already has its client-visible outcome in docs. EVIDENCE: docs/api/rest.md:102 ("returns `INVALID_STATE_TRANSITION` (409), even when its own finalization also failed")
DECISION: docs-alignment lens returns no findings in round 3 — BECAUSE 0085 changes no production behavior and no docs page states the clauses SPEC-1..3 delete — ALTERNATIVES: flagging the STARTING_FAILED "a retry rebinds a fresh pod" row was rejected; it is the pre-existing exclusive-pool stale-binding defect the summary already lists, not something 0085 makes false.


### [non-spec.3.review-edit-sites.1]
FACT: The tree-wide comment sites asserting a §6.2 fresh-pod retry are exactly bindlaunch.go Launch doc, podclaimerror.go writeSetupCommandError doc and tag, podclaimerror_internal_test.go TestWritePodClaimErrorSetupCommandFailed_spec_7_3 annotation (all staged by CODE-1), plus binder.go Bind and resume.go Resume (deliberately left; resume-rebuild callers). The pkg/adapter "a retry can land on a fresh pod" comments describe a generic (client) retry and stay true. EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, pkg/gateway/sessionserver/podclaimerror.go:278-282, pkg/gateway/sessionserver/podclaimerror_internal_test.go:266-273
FACT: docs/ carries no copy of the §15.1 "recovered with a fresh pod" clause, the §4.7 "replacement is claimed" clause, or the §28.5.3 DemoteSDK-deadline row; the error-catalog SETUP_COMMAND_FAILED row already omits fresh-pod recovery. No tests/scripts/claim-map entry quotes any edited spec phrase, and tests/spec-map.json needs no change (both touched tests keep their §6.2 mapping). EVIDENCE: docs/reference/error-catalog.md:128, tests/spec-map.json (6.2 lists start_pod_test.go and TestWritePodClaimErrorSetupCommandFailed_spec_7_3)
FACT: 0105's implementation has not touched any CODE-1/TEST-1 file (diff of impl-0105 vs draft-0085 HEAD is empty for all four), and F-6.2.32 is free in draft-0085, impl-0105, and main BUILD-GAPS.


### [non-spec.3.review-fresh.1]
FACT: Every CODE-1 replace anchor matches the tree verbatim, and the four target files (bindlaunch.go, podclaimerror.go, podclaimerror_internal_test.go, start_pod_test.go) plus create_and_start.go are byte-identical on impl/0105-session-timers, so 0105 landing does not move them — EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, pkg/gateway/sessionserver/podclaimerror.go:278-282, pkg/gateway/sessionserver/podclaimerror_internal_test.go:266-273; `git diff --stat HEAD impl/0105-session-timers` on those files is empty.
FACT: TEST-1's counter discriminates: Binder.Claim -> claimPod builds podclaim.Claimer on b.Client, claimIdle calls CreateClaim(c.Client) per idle sandbox, and the fixture seeds no corev1.Pod, so AdmitTenantPin admits sbx-1 again (NotFound -> admitted) and a regressing re-claim would reach Create — EVIDENCE: pkg/gateway/podlifecycle/podsession/claimconnect.go:46-58, pkg/gateway/podlifecycle/podclaim/claimer.go:214, pkg/gateway/podlifecycle/podclaim/tenantpin.go:85-88, pkg/gateway/sessionserver/start_pod_test.go:183-191.
FACT: F-6.2.32 is free on main, impl/0105-session-timers, and proposal-b/gateway-runtime-comms-remediation (highest is F-6.2.31) — EVIDENCE: BUILD-GAPS.md:5719.
FACT: No test, script, schema, chart, or docs page pins the §15.1 "recovered with a fresh pod" clause or the three replacement-claim spellings SPEC-2/SPEC-3 delete, so no unstaged mirror exists — EVIDENCE: grep over tests/ scripts/ pkg/ schemas/ charts/ docs/ returned nothing.


### [non-spec.3.review-mechanism.1]
FACT: CODE-1 anchors (b)-(d) match the tree verbatim at pkg/gateway/podlifecycle/podsession/bindlaunch.go:21-23, pkg/gateway/sessionserver/podclaimerror.go:278-282, and pkg/gateway/sessionserver/podclaimerror_internal_test.go:267,273. No other pkg/cmd/tests comment asserts a §6.2 fresh-pod retry outside the resume path (grep "fresh pod|§6.2 recovers|transient setup") — EVIDENCE: resume.go:159,309 and binder.go:217,253 are resume/Bind sites the summary deliberately leaves.
FACT: TEST-1's counter mechanism holds: the create-time claim is a typed *lennyv1.SandboxClaim Create through Binder.Client (claimconnect.go:46-47 -> podclaim/claimer.go:215,469), and failPhase only deletes the claim (reclaim.go:53-63), so in envtest sbx-1 stays selectable and a regressing re-claim would call Create again — EVIDENCE: pkg/gateway/podlifecycle/podsession/reclaim.go:60.
FACT: The MCP lenny/create_and_start_session dispatches the REST POST /v1/sessions/start route, so mintClaimStartPersist (create_and_start.go:398, sole caller :88) is the single site F-6.2.32's loop must wrap — EVIDENCE: pkg/gateway/mcpfabric/mcptools/client_tools.go:97.
FACT: F-6.2.32 is free: BUILD-GAPS ends §6.2 at F-6.2.31 (line 5719) before "## §6.3" (5728); no other proposal or the impl-0105 branch files F-6.2.32+.


### [non-spec.3.review-performance.1]
FACT: 0085 changes no production write path. CODE-1 is comments only, TEST-1 is a tier-2 test wrapper, RECORDS-1 is records. There is no etcd, Postgres, or Redis write delta at any tier, and no failover behavior changes. A performance pass has nothing to quantify unless a later round adds a mechanism. EVIDENCE: proposals/0085_.../0085_...non-spec-changes.md "No production behavior changes"
FACT: The round-3 CODE-1 anchors match the tree verbatim: bindlaunch.go:21-23 (Launch doc), podclaimerror.go:278-282 (writeSetupCommandError and its tag line), podclaimerror_internal_test.go:267 and 273. The Bind comments are at binder.go:217 and 253, and the Resume comment is at resume.go:159. EVIDENCE: grep in pkg/gateway/podlifecycle/podsession and pkg/gateway/sessionserver
FACT: The F-6.2.32 ordering claim holds. In create_and_start.go, claimAtCreate (435) and startOnPod (461) run before acquireCoordinationLease (494) and store.Create (501), so a future loop placed ahead of the lease acquire adds no coordination-lease churn per attempt. EVIDENCE: pkg/gateway/sessionserver/create_and_start.go:435-501


### [non-spec.3.review-plan-conformance.1]
FACT: The plan assigns 0085 one queue entry only, gateway-runtime-comms-remediation.md:3303-3306 ("Queue after P4b"): the §6.2 pre-attached retry scope, ordered after 0086, with RECORDS-1's finding renumbered off the colliding F-6.2.27. P2b (:3336) and the §10.4 note (:4344-4346) only sequence it. The P1-P5 "test instruction" (:2835) binds that block, which 0085 is not in. EVIDENCE: gateway-runtime-comms-remediation.md:3303
FACT: F-6.2.32 is free. BUILD-GAPS.md runs up to F-6.2.31 (:5719), and no BUILD-GAPS or TEST-GAPS entry references 0085 yet. The summary's renumbering note has been removed. EVIDENCE: BUILD-GAPS.md:5690-5719
DECISION: plan conformance returns no findings, BECAUSE every plan deliverable for the step is staged (SPEC-1 for the scope, RECORDS-1 for F-6.2.32) — ALTERNATIVES: filing the 0086 ordering was rejected, because ordering is a process requirement rather than a deliverable.


### [non-spec.3.review-reliability.1]
FACT: The F-6.2.32 Impl and Gap text matches the tree. `mintClaimStartPersist` runs claimAtCreate, then startOnPod (on an error, rollbackClaim when createClaimNeedsRollback holds), then acquireCoordinationLease (rollbackBinding on ErrHeld), then store.Create (lease release plus rollbackBinding). A loop placed ahead of the lease acquire therefore holds no lease and no row between attempts — EVIDENCE: pkg/gateway/sessionserver/create_and_start.go:434-512
FACT: TEST-1's counter wrapper sees every SandboxClaim create. claimPod builds a podclaim.Claimer with b.Client (claimer.go:469 `cl.Create`), and fallbackClaim calls podclaim.CreateClaim with b.Client. Passing the counter to podBindBinder therefore counts both paths — EVIDENCE: pkg/gateway/podlifecycle/podsession/claimconnect.go:45-47, fallbackclaim.go:117
FACT: The CODE-1 anchors (bindlaunch.go:21-23, podclaimerror.go:278-282, podclaimerror_internal_test.go:266-273) and the summary defect-list line citations (slot_bind.go:84 and :377, binder.go:217-221, pod_launch.go:224) all resolve in the draft-0085 tree. F-6.2.32 is the next free number after F-6.2.31 (BUILD-GAPS.md:5719, with §6.3 at :5728).


### [non-spec.3.review-security.1]
DECISION: no security findings — BECAUSE 0085 narrows a retry (fewer re-claims), stages comment-only code edits and one tier-2 test counter, and touches no RBAC, NetworkPolicy, admission, tenant pinning, pod-reuse counter, or Redis-backed bound — ALTERNATIVES: considered the §6.2 lead's "released back to the pool" for one-session-only reuse; rejected because SPEC-1 leaves that sentence unchanged, so it is no regression introduced by 0085
FACT: CODE-1 (b)-(d) quoted targets match the tree after the r2 carve fix — EVIDENCE: pkg/gateway/podlifecycle/podsession/bindlaunch.go:22-23, pkg/gateway/sessionserver/podclaimerror.go:279-282, pkg/gateway/sessionserver/podclaimerror_internal_test.go:267,273


### [non-spec.3.review-single-source.1]
DECISION: no single-source findings in round 3 — BECAUSE every repeated statement (the "under this policy" reason, "§7.1/§7.2 defer to §6.2", "CODE-1 limited to falsified comments", the TEST-1 counter reason, abort-out-of-scope) sits at one full home with clause-level mentions elsewhere — ALTERNATIVES: filing the spec-changes vs non-spec "terminal writer during single attempt" edge cases as a copy; rejected, each is two short clauses citing 0082, and the two agree.
FACT: the §6.2 Max retries/Backoff values are restated in F-6.2.32's Spec bullet and quoted in T-6.2.10's Spec/Doc bullet; both are audit-record deliverables whose convention is to state the requirement, so not treated as a (g) copy — EVIDENCE: proposal non-spec-changes RECORDS-1 (a) and (b); spec/06_warm-pod-model.md:297-298
FACT: the spec-changes edge case citing the §6.2 Client visibility "sole exception" for /{id}/start agrees with SPEC-3's "§7.2 starting edges" only once 0105 Edit (g) lands, which rewrites the sole exception to the /start launch taking the §7.2 starting edges — EVIDENCE: proposals/0105_*/0105_*.spec-changes.md:169; spec/06_warm-pod-model.md:302 (pre-0105 text)


### [non-spec.3.review-test-coverage.1]
FACT: TEST-1's counter is discriminating as staged: the create-time claim and any re-claim both go through Binder.Client (sessionserver claimAtCreate -> s.podBinder.Claim, pod_claim.go; podsession claimconnect.go builds podclaim.Claimer{Client: b.Client}; podclaim.CreateClaim calls cl.Create, claimer.go:469). Claimer never mutates Sandbox phase, so sbx-1 stays idle in envtest and a regressing re-claim would reach Create. EVIDENCE: pkg/gateway/podlifecycle/podsession/claimconnect.go:46-48; pkg/gateway/podlifecycle/podclaim/claimer.go:189,469
FACT: TEST-1 anchors exist: start_pod_test.go imports sync/atomic (:18), client (:30), lennyv1 (:35); podBindBinder(c client.Client, ...) at :213; runFinalizeMaterializationFailureCase at :1099. EVIDENCE: pkg/gateway/sessionserver/start_pod_test.go
DECISION: no test-coverage finding this round — BECAUSE no production behavior changes, CODE-1 is comment-only, and TEST-1 pins the one changed clause at tier 2 with a counter that fails on re-claim — ALTERNATIVES: a delegated-child pinning test (already refuted as nice-to-have).

## Retired
