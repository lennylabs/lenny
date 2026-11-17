# Review history: 0157 Persona journey tier

- 2026-11-16, revision 1. Created.
- 2026-11-16, revision 1. Framed from the test infrastructure review in 0154's evidence, at the owner's request to bake its recommendations into the v1 program.
- 2026-11-16, revision 2. The owner answered "ok" to every recommendation. Status `Decided`. The questions as framed in revision 1:

  **Open decisions of revision 1**

  1. **What the agent may read.** Options: (a) only `docs/` and what a persona would see; (b) `docs/` and `spec/`. Recommendation: (a), because a journey then tests the documentation as a reader meets it, and a gap it finds is a documentation gap.
  2. **What a failing journey blocks.** Options: (a) a failing journey blocks the area's milestone exit; (b) it opens an issue and blocks nothing. Recommendation: (b) during the pilot and (a) once the pilot shows the verdicts are stable.
  3. **Budget per run.** The pilot measures cost per journey. Options: (a) set the budget from the pilot's measurements; (b) fix a budget now. Recommendation: (a).
- 2026-11-17, revision 2. The owner settled how the journey agent runs, the spending cap, the cluster, the runtime, the pilot journeys, and the records (decisions 4 to 9).
- 2026-11-17, review of 0157.S1 (general and security). Verified: the agent inherited the host home directory and could reach the owner's other clusters, and a writable kubeconfig let `kubectl` run host commands through an exec credential plugin; a killed run left spend unrecorded; the operator journey named a bare image tag and resource kinds 0137.S5 renames. Fixes that need no decision went to the build. Two questions went to the owner and were settled as decisions 10 and 11.
- 2026-11-17, second review of 0157.S1 (general and security, on the fix commits). The container mounts, environment, image pins, and per-trial scratch held. Fixes that needed no decision, under decision 10: an internal network with an egress proxy that allows only the Anthropic hosts and the trial API server, the journey cluster on its own Docker network with its own pod and service ranges, the admin kubeconfig mounted only when the scenario grants it, a `flock` lock with random run identifiers, leftover containers removed, spend recorded on every path, the CRD-name test reading `pkg/embedded/crds`, and `TESTING.md` corrections. The session read the egress proxy and the commits that fixed the first probe (`846fc46cd`, `c30312acb`).
- 2026-11-17, build. The harness merged into `main` with 0157.S1 `Building`; gate run `afd3221c2b6f2006df2aba4a`. The probe passed (13 checks, $0.0257) after the image stopped setting `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`, which needs bubblewrap and kept Claude Code from starting. The pilot runs from `main`, and its results go into the step's digest when the step lands.
