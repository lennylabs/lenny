# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

1. **O4. The merge gate's time budget for the whole integration package.** Raised by 0116.S3 on 2026-11-18. Decision 3 runs the integration package whole whenever the import graph reaches it, and the framing of O3 moves the split of the integration and chaos tiers ahead of the areas "if it exceeds the gate's budget". No change, rule, or document states that budget: `.claude/rules/test-coverage.md` ("Gates"), TESTING.md ("Merge gate"), and 0154 name the merge gate's tiers and no time limit. The 0116.S3 digest ("Validation") records the whole integration package at 396 to 494 seconds in the gate container across six gate runs, and how often the import graph reaches it over the last 50 merges into `main`.

   - (a) Set no budget now. The merge gate runs the whole integration package whenever the selection reaches it, and the split stays with the areas, as decision 3 states.
   - (b) Set a budget for the merge gate's wall time, such as 30 minutes for a branch that changes one `pkg/` package, and split the integration package ahead of the areas when the measurements of (a) pass it.
   - (c) Split the integration package into one Go package per area now, so package selection narrows it from the start.

   **Recommendation: (a).** The measured run is under ten minutes, the gate runs one branch at a time on this host, and the areas already edit their integration tests, so a split ahead of them repeats that work. 0116.S3 builds (a), which needs no code beyond decision 3; (b) adds a number to `.claude/rules/test-coverage.md` and TESTING.md, and (c) is a change of its own.

2. **O5. The merge-gate review rule and the files that now decide the gate's selection.** Raised by the security review of 0116.S3 on 2026-11-18. `.claude/rules/git-workflow.md` ("Review changes to the merge gate") reviews a branch that touches `cmd/lenny-test/` and the other paths it names, and change 0158 (decision 6) left the `scripts/specshift` packages the harness imports outside it. After 0116.S3 the merge gate's selection also reads `tests/non-go-inputs.yaml`, and its specification-edit half reads `scripts/specshift/annotation`, `ref`, `survey`, and `scope`, so a branch can change what selects its own tests without the review. 0116.S3 bounds both: the selection reads the rows at the diff's base commit beside the branch's, so a branch cannot narrow a row, and a specification-edit selection only adds test functions to what the import graph selects, and fails safe to the merge gate's tiers when it cannot be read.

   - (a) Leave the rule as it is. A branch can still change which test functions a specification edit adds, which the change graph never selected.
   - (b) Add `tests/non-go-inputs.yaml` and the `scripts/specshift` packages the selector imports to the rule's paths, revisiting 0158 decision 6 for those packages.
   - (c) Build the specification-edit selection from the merge base's copy of the tooling, as the rows are read, so the branch's tooling never decides its own selection.

   **Recommendation: (a).** The bounds above leave a branch able to drop only the by-name additions of its own specification edits, and (b) sends every change to the specification tooling through the review that 0158 decision 6 chose to spare it. 0116.S3 builds (a), which needs no edit to the rule.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
