# Decisions: 0157 Persona journey tier

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-16, answering "ok" to each recommendation; the questions as framed in revision 1 are in `review-history.md`.

1. **What the agent reads.** Only `docs/` and what the persona would see: CLI help, API responses, and the user interface.
2. **What a failing journey blocks.** During the pilot, a failing journey opens an issue and blocks nothing. Once three trials of each journey agree consistently, a failing journey blocks its area's milestone exit.
3. **Budget.** The pilot runs at most nine journey runs (three journeys, three trials each), and the budget for later runs is set from its measurements.
