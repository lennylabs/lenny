# Review log: Session suspend, idle suspension, and resume driver

## Standing context

## Ledger

### [f1.human-decisions]
DECISION: The one-proposal-or-split question is resolved as one proposal and withdrawn from summary.md `## Open decisions for human to make`, which now states that no decision is open. The staged files already build the answer (rung 1), so no staged change file was edited. Authority: implementation-checklist.md:26 (S11 CODE-4 depends on S10 CODE-1), non-spec-changes.md:63 (driver "subject to the CODE-1 item 5 age check"), non-spec-changes.md:77 (`LastAgentActivityAt = now` per CODE-6 item 8), spec/06_warm-pod-model.md:260 (accumulated age required), pkg/gateway/runtime/watchdog/watchdog.go:1084 (tree measures `now.Sub(row.CreatedAt)`).
FACT: problem-statement.md `Accepted:` and `Recorded:` lines on the split (lines 121 and 137) were corrected to record the split as rejected, so they agree with the summary.

### [f1.other-proposals]
DECISION: summary.md `## Impacts on other proposals` row 0085 corrected from Implemented to Draft (drafted 2026-09-27, not converged). It now states that deleting `RESUME_FAILED` (SPEC-3c) and the store-write `/resume` (SPEC-3b) remove the subject of 0085's resume recovery bullet and of its staged §15.1 fallback text, that its create and start stop-condition work survives, and that 0085 must rebase and drop `RESUME_FAILED`. Authority: proposals/0085_fix_the-6-2-pre-attached-failure-retry-loop-has/0085_fix_the-6-2-pre-attached-failure-retry-loop-has.status.md:5 (status: Draft), 0085 spec-changes.md:27, 63, and 69, and this proposal's spec-changes.md:119 and 125.
WATCHOUT: 0085 non-spec-changes.md also edits comments in `holdOrFailOnResumeError`, which CODE-4 deletes; the row's rebase instruction covers it.

### [f1.other-proposals-0077]
DECISION: summary.md `## Impacts on other proposals` gained row 0077 (Draft, last commit 2026-08-19): SPEC-5g bounds suspended sessions with `gateway.maxSuspendedSessionSeconds` and adds `suspended → expired`, falsifying 0077's §10 premise that a parked root `suspended` session may remain indefinitely; no 0077 deliverable loses its subject; 0077 corrects the premise when next revised. Authority: proposals/0077_new_pre-materialize-a-workspace-on-a-warm-pod.md:234-238, spec/06_warm-pod-model.md:287, this proposal's spec-changes.md:333 and 383-386.
FACT: The row omits the reading's "only if option B ships" condition because the split question was resolved as one proposal (summary.md `## Open decisions for human to make`).
