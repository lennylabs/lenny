# Steps: 0114 Helpers

## Validation owed

| Item | Waits on it |
| :-- | :-- |
| The owner's time and the tokens each of the first few changes after this one uses, so the helpers can be adjusted from those numbers ([WORKFLOW.md](../0115-spec-structure/evidence/spike/WORKFLOW.md), "Build order"). | Nothing; it is recorded in each change's digest. |

## Steps

### 0114.S1 Move the step checker, board generator, and link rewriter into scripts/changes, and add the change commands
- status: Building
- lanes: [non-specification]

`scripts/changes/` with `steps.py`, the board generator, the link rewriter and link checker from 0113's `restructure.py`, and `change.py` with `new`, `status`, `step`, `archive`, `check`, and `board`, with tests run by tier 0. The `change` skill calls it. 0113's evidence keeps its copies as the record of what ran.

### 0114.S2 Generic skills
- status: Planned
- lanes: [non-specification]
- depends-on: [0114.S1]

The `tidy`, `fact-check`, `ask`, and `change` skills, and the helpers table in `changes/README.md` naming each helper's file.

### 0114.S3 The review skill
- status: Planned
- lanes: [non-specification]

`.claude/skills/review/SKILL.md` with its general and security subagents, `--since`, `--fix`, and `--record`. It is tried on one moved design before it is relied on.

### 0114.S4 The build skill
- status: Planned
- lanes: [non-specification]
- depends-on: [0114.S1]

The `build` skill: the branch name, the order of the lanes, `lenny-test --changed`, the step status, the hand-written digest, and the stop on an unsettled choice.

### 0114.S5 Retire the proposal pipeline and rewrite the governing rule
- status: Planned
- lanes: [non-specification]
- depends-on: [0114.S2, 0114.S3, 0114.S4]

Delete the pipeline's workflows, skills, tools other than `lookup`, the `cp-writer` agent, the plan file, and the tests other than `lookup.test.mjs` and the files it needs. Remove their rows from the residual registers, rewrite `spec-driven-development.md`, and update `AGENTS.md` where it names a retired helper. Tier 0 passes.
