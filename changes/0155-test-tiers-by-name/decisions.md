# Decisions: 0155 Test tiers by name and the static and documentation tiers

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-16, answering "ok" to each recommendation; the questions as framed in revision 1 are in `review-history.md`.

1. **Names.** Every tier is named by what it covers, as `lenny-test` already names it, in directories, packages, and prose, with no split tiers. `TESTING.md` keeps a table from each old number to its name, and historical records keep the numbers.
2. **Deletions without one-for-one replacement.** Accepted: the tier-0 category D gates, the documentation-tier reconciliation tests, and the gap-record tests go, and each area change confirms its requirements keep a test.
3. **Timing.** The rename runs right after 0115 merges and before any area change starts.
