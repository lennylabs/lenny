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


## Retired
