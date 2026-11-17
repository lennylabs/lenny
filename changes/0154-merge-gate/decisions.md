# Decisions: 0154 Green main line and merge gate

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-16, answering "ok" to each recommendation; the questions as framed in revision 1 are in `review-history.md`.

1. **Canonical environment.** The merge gate runs in a Linux container, built from the repository with the gate's toolchain pinned. Developers on macOS run the gate inside it.
2. **The `pr` workflow.** Disabled until pull requests replace direct merges, then repaired to run the merge gate in the same container.
3. **Coverage target.** 80 percent line coverage on new code stays as a floor, and a reviewer may waive it for wiring code, naming what was waived. A scheduled mutation run on a short list of critical packages (claims, admission, credentials) is reported per package and gates nothing.
4. **Gates and the tier list.** The list in `test-coverage.md` keeps saying which tiers' tests a change writes. The merge, area, and release gates replace its instruction on which tiers run and what must pass before a merge.
5. **Sweep.** The files deleted by design-only commits since 2026-10-01 are listed and checked in this change.
