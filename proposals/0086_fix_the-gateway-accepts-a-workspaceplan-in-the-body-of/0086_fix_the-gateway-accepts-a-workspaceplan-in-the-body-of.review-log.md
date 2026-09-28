# Review log: Finalize-time WorkspacePlan bind has no specification contract

## Standing context

### Settled

### Traps

### Open

### Deferred

## Ledger

### [f1.impacts]

DECISION: Impact row for draft 0085 rewritten in summary `## Impacts on other proposals`: status now carries its date (Draft, drafted 2026-09-27, per proposals/0085_fix_the-6-2-pre-attached-failure-retry-loop-has/0085_fix_the-6-2-pre-attached-failure-retry-loop-has.status.md:5-6), the row names 0086's `WORKSPACE_PLAN_INVALID` and `GIT_CLONE_*` catalog rows against 0085's `SETUP_COMMAND_FAILED` row, and it records that 0085's one-attempt `/finalize` rule (0085 spec-changes.md:53) composes with the rejected-finalize rule (0086 spec-changes.md:114). Action stays "Nothing". No checklist change.
FACT: handleFinalize resolves the plan (pkg/gateway/sessionserver/sessionserver.go:3120) before the created-to-finalizing Update and before prepareAtFinalize (:3163), so a plan rejection never reaches pre-attached work.
DECISION: Impact row for draft 0084 rewritten in summary `## Impacts on other proposals`: status now carries its date (Draft, drafted 2026-09-26, per proposals/0084_fix_on-a-pool-with-sessionpolicy-maxconcurrentsessions-1-one-run/0084_fix_on-a-pool-with-sessionpolicy-maxconcurrentsessions-1-one-run.status.md:5-6). The row names the disjoint co-edits in spec/29 §29.2 (0084 steps 24, 26a, and 26b per 0084 spec-changes.md:376, :390, and :416; 0086 steps 14 and 15 per 0086 spec-changes.md:260), spec/15, openapi.json, and docs/getting-started/concepts.md. It replaces the wording that implied 0084 touches `LennyBlobURI` with 0084's spec/28 edits (0084 spec-changes.md:352), a file 0086 does not edit (0086 spec-changes.md:387-394). Action stays "Nothing". No checklist change.
DECISION: Impact row for draft 0077 added to summary `## Impacts on other proposals`, between the 0082 and 0007 rows: status Draft (dated 2026-08-19, per proposals/0077_new_pre-materialize-a-workspace-on-a-warm-pod.md:3 and :8, and commit 8364087f9). The row records that 0077's §1.3 pin-timing restatement ("at session creation", 0077:91-92, matching spec/14_workspace-plan-schema.md:102) goes stale under SPEC-1 edit (c) (0086 spec-changes.md:57-66), while the per-session immutability sentence (spec/14_workspace-plan-schema.md:104) stays unedited, so 0077's argument and design sketch stand. Action: reword the pin-timing sentence to cite §14.1 **Plan binding.** if 0077 is revived after 0086 lands. No checklist change.

### [f1.cleanup]
FACT: summary.md already carried exactly the listed sections in order: `# Summary: Finalize-time WorkspacePlan bind has no specification contract`, `## Summary` (holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, and `**Watch out for.**` in that order), `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and `## Deliverable index` (last). This pass rewrote nothing, relocated nothing, and deleted nothing. No rename was owed, and `**Problem statement.**` and `## Deliverable index` were not touched.
FACT: `## Open decisions for human to make` carries no entry, no retired block, and no meta-list of staged items. Its preamble "None yet. The review loops write this section." stays true after firing 1, so it was left as written. Every item this firing adjudicated left as an out-of-scope defect already carried in `## Defects in the shipped tree that this proposal does not stage` or as a row of `## Impacts on other proposals` (0085, 0084, and 0077), and none is the human's.
FACT: the review log carried no `## Retired` section, so this block closes `## Ledger` at the end of the file.
FACT: the gate and the apply step refuted the out-of-scope item for the defect entry "A running-state blob upload has no consumer", and the entry stands unchanged because this pass promotes defect entries verbatim. In the tree, `admitUpload` calls `session.Validate` for `session.EndpointUpload` without `Capabilities`, so the `CapabilityMidSessionUpload` gate in `preconditionTable` never lifts and `/upload` and `/upload-archive` reject a `running` session. The §15.1 `/upload` precondition row admits `running` under `midSessionUpload`, so the disagreement is between that spec row and the tree rather than an unconsumed running-state blob. — EVIDENCE: pkg/gateway/sessionserver/upload.go:284-318, pkg/api/v1/session/session.go:282-287, spec/15_external-api-surface.md:645
OPEN: the defect entry "A running-state blob upload has no consumer" in summary `## Defects in the shipped tree that this proposal does not stage` states that a blob uploaded in `running` is never placed, while the tree rejects a `running`-state `/upload` (see the FACT above). The next firing decides whether to reword the entry as a spec-row and tree disagreement or to remove it.
