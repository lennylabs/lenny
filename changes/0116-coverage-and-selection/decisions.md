# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

1. **A move that rewrites only links (0116.S2).** CS-3 leaves out a requirement that moves with its text unchanged. The gate compares a requirement's text as the page states it, so when a page moves to a folder at another depth and a relative link inside a requirement is rewritten to keep resolving, the gate reads the requirement as modified and asks for a citing test or an open issue. The failure is visible and never a false pass, and the registry holds no requirement yet, so no branch meets the case today.
   - (a) Keep the exact comparison, and state in the "Coverage gate" part of TESTING.md that a rewritten link counts as a change of text. A requirement that already has a citing test passes regardless.
   - (b) Compare the text with each link target resolved to a path from the repository root, so a move that keeps every link pointing at the same file is read as unchanged. The digest and `since` use the same comparison and would change with it.
   - Recommendation: (a). It needs no new code, every requirement an area adds carries a citing test under the coverage gate anyway, and the digest stays consistent with the gate. The branch builds (a); (b) can follow when an area's move meets the case.

2. **Measuring the area exit for section-numbered test names (0116.S7).** CS-10 adds to the v1 area exit that an area's test names carry no section number, and leaves existing names to each area. The test-name lint reads changed lines, so the existing names do not fail it, and CS-10 names no register or ratchet for them. What shows an area that it has reached zero is unsettled.
   - (a) No register. The area change counts the names in its packages with the lint's matcher when it closes, and the area's review checks the count. Nothing is committed for the existing names.
   - (b) A per-package ratchet beside `tests/registers/spec-section-citations.yaml`, counting test names that carry a section number, which fails a package whose count rises and writes a lower count back.
   - (c) A count of such names per package in the coverage view (`lenny-test coverage --spec`), computed on demand from the annotation index, with no committed file.

   Recommendation: (a) now, and (c) if an area finds the count hard to take. The lint already refuses a section number in a new or changed name, so a ratchet would add a register of the kind 0116 retires elsewhere (CS-9), and a count in the coverage view can be added later without changing the lint. 0116.S7 as built adds nothing for the existing names, which is consistent with (a) and (c); option (b) would add a ratchet to 0116.S7 before it lands.

3. **The merge gate's time budget for the whole integration package.** Raised by 0116.S3 on 2026-11-18. Decision 3 runs the integration package whole whenever the import graph reaches it, and the framing of O3 moves the split of the integration and chaos tiers ahead of the areas "if it exceeds the gate's budget". No change, rule, or document states that budget: `.claude/rules/test-coverage.md` ("Gates"), TESTING.md ("Merge gate"), and 0154 name the merge gate's tiers and no time limit. The 0116.S3 digest ("Validation") records the whole integration package at 396 to 494 seconds in the gate container across six gate runs, and how often the import graph reaches it over the last 50 merges into `main`.

   - (a) Set no budget now. The merge gate runs the whole integration package whenever the selection reaches it, and the split stays with the areas, as decision 3 states.
   - (b) Set a budget for the merge gate's wall time, such as 30 minutes for a branch that changes one `pkg/` package, and split the integration package ahead of the areas when the measurements of (a) pass it.
   - (c) Split the integration package into one Go package per area now, so package selection narrows it from the start.

   **Recommendation: (a).** The measured run is under ten minutes, the gate runs one branch at a time on this host, and the areas already edit their integration tests, so a split ahead of them repeats that work. 0116.S3 builds (a), which needs no code beyond decision 3; (b) adds a number to `.claude/rules/test-coverage.md` and TESTING.md, and (c) is a change of its own.

4. **The merge-gate review rule and the files that now decide the gate's selection.** Raised by the security review of 0116.S3 on 2026-11-18. `.claude/rules/git-workflow.md` ("Review changes to the merge gate") reviews a branch that touches `cmd/lenny-test/` and the other paths it names, and change 0158 (decision 6) left the `scripts/specshift` packages the harness imports outside it. After 0116.S3 the merge gate's selection also reads `tests/non-go-inputs.yaml`, and its specification-edit half reads `scripts/specshift/annotation`, `ref`, `survey`, and `scope`, so a branch can change what selects its own tests without the review. 0116.S3 bounds both: the selection reads the rows at the diff's base commit beside the branch's, so a branch cannot narrow a row, and a specification-edit selection only adds test functions to what the import graph selects, and fails safe to the merge gate's tiers when it cannot be read.

   - (a) Leave the rule as it is. A branch can still change which test functions a specification edit adds, which the change graph never selected.
   - (b) Add `tests/non-go-inputs.yaml` and the `scripts/specshift` packages the selector imports to the rule's paths, revisiting 0158 decision 6 for those packages.
   - (c) Build the specification-edit selection from the merge base's copy of the tooling, as the rows are read, so the branch's tooling never decides its own selection.

   **Recommendation: (a).** The bounds above leave a branch able to drop only the by-name additions of its own specification edits, and (b) sends every change to the specification tooling through the review that 0158 decision 6 chose to spare it. 0116.S3 builds (a), which needs no edit to the rule.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
