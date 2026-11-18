# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

**O4. The merge gate's time budget for the whole integration package.** Raised by 0116.S3 on 2026-11-18. Decision 3 runs the integration package whole whenever the import graph reaches it, and the framing of O3 moves the split of the integration and chaos tiers ahead of the areas "if it exceeds the gate's budget". No change, rule, or document states that budget: `.claude/rules/test-coverage.md` ("Gates"), TESTING.md ("Merge gate"), and 0154 name the merge gate's tiers and no time limit. The 0116.S3 digest ("Validation") records the whole integration package at 396 to 494 seconds in the gate container across six gate runs, and how often the import graph reaches it over the last 50 merges into `main`.

- (a) Set no budget now. The merge gate runs the whole integration package whenever the selection reaches it, and the split stays with the areas, as decision 3 states.
- (b) Set a budget for the merge gate's wall time, such as 30 minutes for a branch that changes one `pkg/` package, and split the integration package ahead of the areas when the measurements of (a) pass it.
- (c) Split the integration package into one Go package per area now, so package selection narrows it from the start.

**Recommendation: (a).** The measured run is under ten minutes, the gate runs one branch at a time on this host, and the areas already edit their integration tests, so a split ahead of them repeats that work. 0116.S3 builds (a), which needs no code beyond decision 3; (b) adds a number to `.claude/rules/test-coverage.md` and TESTING.md, and (c) is a change of its own.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
