# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
