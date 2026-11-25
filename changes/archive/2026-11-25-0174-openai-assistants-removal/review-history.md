# Review history: 0174 Removal of the openai-assistants reference runtime

- 2026-11-25, revision 1. Created with decision 1, the owner's answer of 2026-11-20 to question DV-1 of the M3 readiness decision page, recorded as decision 12 of program [v1](../../programs/v1.md#decisions). Status `Decided`.
- 2026-11-25, 0174.S1. Status `In progress`: step 0174.S1 started on branch `change/0173-0174-housekeeping`, which it shares with 0173.S1 at the owner's request.
- 2026-11-25, 0174.S1, review. One round (general and security) over `523ea2b27..c0db4221d`. Revision 2 states how the design text of 0118, 0122, 0135, and 0137 is handled, rather than citing a rule of the v1 program that does not cover it, and 0135's "Validation owed" drops the Assistants API (general 5). `docs/about/comparisons.md` drops a stale count of framework adapters (general 4), and the `RuntimeOptionsSchemaRef` comment's mid-line annotation becomes prose like its sibling's (general 9). The `F-26.10.2` finding identifiers stay in the comments that list the findings the code closed.
- 2026-11-25. Step 0174.S1 landed (gate run `d3dcc8ba00e6fef49bb6eaf5` on `main`), and the change is `Implemented`.
