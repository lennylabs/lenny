# Review history: 0155 Test tiers by name and the static and documentation tiers

- 2026-11-16, revision 1. Created.
- 2026-11-16, revision 1. Framed from the test infrastructure review in 0154's evidence, at the owner's request to bake its recommendations into the v1 program.
- 2026-11-16, revision 2. The owner answered "ok" to every recommendation. Status `Decided`. The questions as framed in revision 1:

  **Open decisions of revision 1**

  1. **Names or renumbered numbers.** Options: (a) name every tier by what it covers, as `lenny-test` already does, in directories, packages, and prose; (b) renumber 0 to 13 with no split tiers. Recommendation: (a), because names carry their meaning, a tier added later needs no renumbering, and the order a number implies moves to the merge, area, and release gates. The blast radius is about 1,460 files, almost all rewritten by a script.
  2. **Deleting tests without a one-for-one replacement.** The tier-0 category D gates, the bespoke documentation-tier reconciliation tests, and the gap-record tests go, and some carry `// spec:` annotations, so the derived coverage view will show sections losing a test. Options: (a) accept that, since the requirements they shadow are tested elsewhere or are prose consistency checks the 0115 gates replace; (b) require a replacement test for each annotated one. Recommendation: (a), with each area change checking that its requirements keep a test.
  3. **Timing.** The rename touches every test directory, and 0115's branches and the area changes edit `tests/static/`. Options: (a) run the rename as the first step after 0115 merges and before any area change starts; (b) run it at the end of v1. Recommendation: (a), because every later change then writes the new names.
- 2026-11-17, build. Steps S1 to S3 built on one branch; the merge gate passed on its head (run `652a1c8aac7fb32edd996f46`) after the gate-environment follow-up merged into `main`. [`digests/0155.S1-S3.md`](digests/0155.S1-S3.md) records the result. Status `Implemented`.
