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

## Decisions

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Coverage gate.** A requirement a diff adds or modifies passes when a test cites it or an open issue names it.
2. **Claim register.** Its 41 rows that are not `WIRED` are filed as issues now, and `tests/claim-map.json` retires.
3. **Single-package tiers.** The integration and chaos tiers run whole when selected now, and are split per area as areas migrate.
