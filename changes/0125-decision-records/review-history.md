# Review history: 0125-decision-records Decision records

# Revision notes

- Revision 1, 2026-11-16. Written from the owner's words "Actually I've been wondering whether this whole ADR thing is needed at all" and the owner's "ok" to removing ADRs through a separate short design (D1). The sites were found by searching the tree for `ADR`, `ADRs`, `ADR-` numbers, `docs/adr`, `adr/`, and `check-adr`, outside `proposals/` and the audit records, and each was read in place. While writing it, the author found that the two designs that planned an ADR already had their rows removed in the working tree by a concurrent pass, so "Instructions for other designs" states the instruction for the record. No independent review has read revision 1.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/decision-records-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location. Sentences that named `designs/` as the current home of designs now name `changes/`; dated statements of what was read under `designs/` keep their words.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> with no independent review yet. Nothing here is built or staged. It records the owner's decision of 2026-11-16 to remove Architecture Decision Records (ADRs) from Lenny (D1), and lists every site that names them with the change each site takes. Changes under `changes/` and change proposals under `proposals/` become Lenny's decision records, and §19 stays as the summary table of resolved decisions. It reads against every design under `designs/` as implemented, in particular [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) revision 3, which renames the claim kind inside the §4.6.1 paragraph whose "ADR-007" label this design removes. The changes go through `change-proposal` after review. The design element carries the label REC-1, and requirements carry the label RECR-n. "Review history" at the end records the revisions. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## M3 readiness review (2026-11-19)

Revision 2, 2026-11-19. Trigger: review. The M3 readiness review read revision 1 against `main` at `092ef92f6`, with the consistency reports of the program's clusters. No finding was High, so no skeptic's verdict applies. The review raised three questions for the owner (0125-Q1 to 0125-Q3). The consistency reports settle each from records that already govern: tenancy-security-C12 states that no owner decision is needed because D1 and change 0113 settle the workflow, and tenancy-security-C13 resolves §19 and the claim fencing paragraph through decision 9 of change 0115 and its migration ledger, with an owner question only if the owner wants §19 kept permanently. This entry applies those resolutions. Counts: 13 applied, 0 raised as open decisions, 0 returned as issues, and 0 dropped. One edit to the program is returned to the review rather than made here.

Applied:

- 0125-F1 and tenancy-security-C12: the quoted block of "REC-1 Removal", the "Terms" rows for a change and a change proposal, "High-level design", "Scope", RECR-3, RECR-5, the §19 row 14 and closing-paragraph edits, the §4.6.1 test 1 wording, and the documentation rows for `docs/about/contributing.md`, `docs/about/governance.md`, `GOVERNANCE.md`, `docs/getting-started/index.md`, `TESTING.md`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/feature_request.md`, `CHANGELOG.md`, and `.claude/rules/doc-content.md` name a change under `changes/` as the record of a decision and the way to change one, with the statuses of `changes/README.md`, and name `proposals/` and `changes/archive/` as historical records. The published text no longer names `designs/` or cites a section number, and the "Terms" row no longer names `.claude/tools/proposal-status.mjs`.
- 0125-F2 and tenancy-security-C13 (§19): "Terms", "Scope", "High-level design", "REC-1 Removal", RECR-3, and the two §19 rows state that the §19 edits are transitional and that the area changes distribute §19 under decision 9 of change 0115. The `LICENSE` file and `docs/about/governance.md` ("License") are named as the home of the license decision, and the published text no longer links §19.
- 0125-F3 and tenancy-security-C13 (§4.6.1): "Scope", "REC-1 Removal", RECR-4, and the §4.6.1 row state that 0125.S1 removes the ADR wording only, and that the paragraph's content goes where the runtimes-and-pools area change places it when it migrates §4.6.1, citing the 0115 migration ledger (the two tests to `TESTING.md`, the fencing argument as the rationale of a `CLAIM` requirement).
- 0125-F4: "Non-specification changes" gains a row for `cmd/lenny-test/cmd_gate.go` (`"adr_test.go"` leaves `gateDocsFiles`, and the comment drops "ADRs"), the `TESTING.md` row adds "Merge gate", 0125.S2 states that the branch is reviewed with the `review` skill under `.claude/rules/git-workflow.md` and that its digest records the outcome, and "Validation owed" gains the row "`lenny-test gate` returns `PASS` on the head of the 0125.S2 branch".
- 0125-F5: "Tests" drops the two prose-matching checks and states that review verifies RECR-3 and RECR-5, keeps the absence check and the existing link gate, names the tiers `static`, `chaos`, and `docs`, and states that the absence check carries no `// spec:` annotation.
- 0125-F6: "Current state" gains a dated list of what the tree holds on 2026-11-19. "Specification changes" drops the moot §18 and §23 rows with a note, "Non-specification changes" corrects the paths (`pkg/podclaim/state/state.go`, `pkg/admission/podclaim_guard/guard.go`, `pkg/admission/webhook/podclaim_guard.go`, `podclaim-guard-webhook.yaml`, `pkg/gateway/core/podclaim/`, and `tests/static/build/license_test.go`), drops the retired `tests/spec-map.json` and `tests/spec-map-exceptions.yaml` rows with a note, and adds a row for `tests/static/lint/pod_lifecycle_kind_names_test.go`. "Documentation" drops `ROADMAP.md` and the "Foundation" milestone row, and uses the current `TESTING.md` headings. "Problem" and "Terms" name `TestPodClaimRaceUnder100Goroutines` and the `PodClaim` label.
- 0125-F7: 0125.S1 states that it lands before the first area change that migrates §4.6.1, §6.3, §16.5, or §19, that an area change that migrates first carries the edit onto its page, and that 0125.S1 and 0125.S2 may share one branch and one digest. "Specification changes" states the successor rule.
- 0125-F8: the §6.3 and §16.5 rows record the benchmark results in the evidence folder of the change that runs the benchmark, and 0125.S1 carries `closes: [F-6.3.20 (part), T-6.3.20 (part)]` with a sentence naming the part.
- 0125-F9: the `static` check matches `\bADRs?\b`, `ADR-[0-9]+`, `docs/adr`, and "Architecture Decision Record", exempts `changes/`, `proposals/`, `issues/`, the audit records, `CODE-REVIEW.md`, `CHANGELOG.md`, and `testdata/`, lives in `tests/static/lint`, and spells its patterns without matching itself.
- 0125-F10: the `GOVERNANCE.md` row renames the heading "Architecture Decision Records" to "Decision records", links `docs/about/governance.md#decision-records`, and counts six sites.
- 0125-F11: "REC-1 Removal" (**Tests and tooling.**) and the comment rows cite §4.6.1 as `§4.6.1 (Warm Pool Controller (Pod Lifecycle))` in a `// spec:` annotation, or the `CLAIM` requirement once its page lands, and name the bold label in prose comments, `// diagnosis:` lines, and failure messages.
- 0125-F12: "Validation owed" gains a row to run `lenny-test --tier chaos` for `TestLeaderKillMidClaimNoDoubleAssignment` after the rename, or record the precondition, and the rename row states that the two `skip-reasons.yaml` entries key the test by path.
- 0125-F13: this review is the first independent review of revision 1. Revision 2 has had no independent review, and the `review` skill reads it before the status moves to `Decided`.

Raised as open decisions: none. 0125-Q1, 0125-Q2, and 0125-Q3 of the review are settled by the records the consistency reports name. If the owner wants §19 kept permanently, that amends decision 9 of change 0115 and reopens 0125-Q2.

Returned as an edit elsewhere:

- `changes/programs/v1.md`, "Reading the program": an edit to an item of §19, §20, or §22 applies where the area change placed it, as its successor stub states (program-C12), and the M3+ order places 0125.S1 before the area changes that migrate §4.6.1, §6.3, §16.5, and §19, or those area changes carry its edits (0125-F7).

Dropped: none.

## Status under the owner's answer to PG-C5 (2026-11-23)

The status becomes `Decided`. The owner's answer of 2026-11-19 to question PG-C5 of the M3 readiness decision page (option (a), program-C5) sets every member change with no open decision and no started step to `Decided`, and this change has no open decision and no started step. The revision stays 2, because no content changed.
