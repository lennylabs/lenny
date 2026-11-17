# Decisions: 0154 Green main line and merge gate

## Open decisions

From the open questions of the [review](evidence/test-infrastructure-review.md):

1. **Canonical environment.** Options: (a) a Linux container that macOS developers run the gate in; (b) the macOS host, with every Linux-only test skipping off Linux. Recommendation: (a), because the gate's verdict then means the same thing on every machine and in CI.
2. **The `pr` workflow.** Options: (a) repair `.github/workflows/pr.yml` to run the merge gate in the same container now; (b) disable it until pull requests replace direct merges. Recommendation: (b) now and (a) when pull requests start, because merges are direct until v1 ships and a workflow that always fails teaches everyone to ignore it.
3. **Coverage target on new code.** Options: (a) keep 80 percent line coverage as a hard rule; (b) keep it as a floor reviewers can waive for wiring; (c) replace it with a mutation score on critical packages. Recommendation: (b), with a scheduled mutation run on critical packages reported per package.
4. **Gates and the tier list.** Options: (a) the merge, area, and release gates replace the "Which tiers a change must touch" list in `test-coverage.md`; (b) they sit beside it. Recommendation: (a), because one list of what must pass when is easier to follow than two.
5. **Sweep for accidental deletions.** Options: (a) sweep deletions in design-only commits since 2026-10-01 in this change; (b) skip it. Recommendation: (a), because the boilerplate deletion went unnoticed for nine days.

## Decisions
