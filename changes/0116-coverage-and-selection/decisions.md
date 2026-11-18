# Decisions: 0116 Coverage and test selection from annotations

## Open decisions

1. **A move that rewrites only links (0116.S2).** CS-3 leaves out a requirement that moves with its text unchanged. The gate compares a requirement's text as the page states it, so when a page moves to a folder at another depth and a relative link inside a requirement is rewritten to keep resolving, the gate reads the requirement as modified and asks for a citing test or an open issue. The failure is visible and never a false pass, and the registry holds no requirement yet, so no branch meets the case today.
   - (a) Keep the exact comparison, and state in the "Coverage gate" part of TESTING.md that a rewritten link counts as a change of text. A requirement that already has a citing test passes regardless.
   - (b) Compare the text with each link target resolved to a path from the repository root, so a move that keeps every link pointing at the same file is read as unchanged. The digest and `since` use the same comparison and would change with it.
   - Recommendation: (a). It needs no new code, every requirement an area adds carries a citing test under the coverage gate anyway, and the digest stays consistent with the gate. The branch builds (a); (b) can follow when an area's move meets the case.

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
