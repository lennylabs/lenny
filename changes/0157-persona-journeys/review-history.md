# Review history: 0157 Persona journey tier

- 2026-11-16, revision 1. Created.
- 2026-11-16, revision 1. Framed from the test infrastructure review in 0154's evidence, at the owner's request to bake its recommendations into the v1 program.
- 2026-11-16, revision 2. The owner answered "ok" to every recommendation. Status `Decided`. The questions as framed in revision 1:

  **Open decisions of revision 1**

  1. **What the agent may read.** Options: (a) only `docs/` and what a persona would see; (b) `docs/` and `spec/`. Recommendation: (a), because a journey then tests the documentation as a reader meets it, and a gap it finds is a documentation gap.
  2. **What a failing journey blocks.** Options: (a) a failing journey blocks the area's milestone exit; (b) it opens an issue and blocks nothing. Recommendation: (b) during the pilot and (a) once the pilot shows the verdicts are stable.
  3. **Budget per run.** The pilot measures cost per journey. Options: (a) set the budget from the pilot's measurements; (b) fix a budget now. Recommendation: (a).
