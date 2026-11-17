# Review history: 0114-helpers

- 2026-11-16, revision 1. Skeleton created by 0113.

- 2026-11-16, revision 2. Framed: problem, scope, the helpers' design, the current pipeline's inventory, six open decisions with recommendations, and five steps.

- 2026-11-16, revision 3. The owner settled the six open decisions: the recommendations stand, except that `review` is a skill with two subagents, one for a general review and cross-reference and one for security. The questions as framed in revision 2 follow.

  **Open decisions of revision 2**

  1. **Retire the proposal pipeline in this change.** Delete its workflows, skills, tools, agent, plan file, and tests, and their register rows. Git history keeps them, the open proposals are set aside, and the changes README already replaces each piece ([WORKFLOW.md](../2026-11-16-0115-spec-structure/evidence/spike/WORKFLOW.md), "What retires"). Options: (a) delete now; (b) keep them until the open proposals are revisited. Recommendation: (a).
  2. **Rewrite `spec-driven-development.md` in this change.** The governing rule still routes every specification change through `change-proposal` and `implement-proposal`. Options: (a) rewrite it here as "Detailed design" describes; (b) leave it to 0115, which restructures the specification. Recommendation: (a), because an agent following the rule today uses the retired loop.
  3. **Form of each helper.** Options: (a) `review` as a workflow, the others as skills, and the bound helpers' model-free logic in Python under `scripts/changes/`; (b) every helper as a skill, with `review` starting its reviewers as subagents. Recommendation: (a), because a workflow runs the reviewers and verifiers in a fixed order and its tests can replay it.
  4. **Move the step checker and board generator out of 0113's evidence now.** Options: (a) move them to `scripts/changes/` in this change as the `board` helper, with tests; (b) leave them in 0113's evidence until 0115. Recommendation: (a), because `change` and `build` call them and the board is already in use.
  5. **Defaults of `review`.** Options: (a) three default charters (tree checker, implementer, documentation reader), a security reviewer on request, one verifier per finding, one round, and `--fix` limited to findings that need no decision; (b) the same with no verifier, to halve the cost. Recommendation: (a), because unverified findings were the main source of churn in the pipeline's rounds.
  6. **Branches and the integration branch.** Every change so far sits on `design/v1-cleanup` or a branch cut from it, and none is merged into `main`. Options: (a) merge `change/0113-records-and-workflow` into `design/v1-cleanup`, then `design/v1-cleanup` into `main`, and cut each later step's branch, `change/<step>-<slug>`, from `main`; (b) keep stacking branches until M0 ends. Recommendation: (a), as `.claude/rules/git-workflow.md` states.


- 2026-11-16, build. Steps S1 to S5 built on `change/0114-helpers`; [`digests/0114.S1-S5.md`](digests/0114.S1-S5.md) records the result. The `review` skill was tried on 0132: two subagents, about 273,000 tokens, every finding's evidence confirmed. The steps stay `Building` until the owner merges the branch.
- 2026-11-16, merge. The owner closed the change: every step `Landed`, status `Implemented`, archived, and merged into `main`.
