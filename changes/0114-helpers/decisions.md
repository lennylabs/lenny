# Decisions: 0114 Helpers

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-16 ("go with all your recommendations, except when it comes to `review`"):

1. **The proposal pipeline retires in this change.** Its workflows, skills, tools other than `lookup`, agent, plan file, and tests are deleted with their register rows. Git history keeps them.
2. **`spec-driven-development.md` is rewritten in this change** around `changes/`.
3. **Form of the helpers.** Every helper is a skill, `review` included. The model-free logic of the bound helpers is Python under `scripts/changes/`.
4. **The step checker, board generator, and link rewriter move to `scripts/changes/`** in this change, with tests.
5. **`review` runs two subagents.** In the owner's words: "run it as a skill that spins up different subagents. Have one subagent for a general review of the change itself and a cross-reference to the tree, docs, as well as any other staged/planned changes in the same program. The other subagent should run a security review." This replaces the recommendation of three charters with a verifier per finding.
6. **Branches.** `change/0113-records-and-workflow` merges into `design/v1-cleanup`, and `design/v1-cleanup` into `main`, each with `--no-ff`. Each later step's branch, `change/<step>-<slug>`, is cut from `main`.

The questions as framed in revision 2 are in `review-history.md`.
