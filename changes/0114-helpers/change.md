# Helpers

Status: `Draft`, revision 2, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M0.

## Problem

The proposal pipeline converges each proposal through many reviewer lenses and rounds. It is 11 workflows under `.claude/workflows/` (19,453 lines), 3 skills, 10 tools under `.claude/tools/` (3,951 lines), the `cp-writer` agent, and 26 test files under `.claude/tests/`. In proposals 0082 to 0112 the review log is 70 to 85 percent of a proposal's words. The owner replaced the pipeline on 2026-11-16 with a lighter loop and a set of helpers that the owner uses as needed ([changes README](../README.md), "Workflow" and "Helpers").

The governing rule, `.claude/rules/spec-driven-development.md`, still tells agents to change the specification through `change-proposal` and `implement-proposal`, so an agent that follows it today uses the retired loop.

## Scope

- **In scope.**
  - The generic helpers `review`, `tidy`, `fact-check`, and `ask`.
  - The bound helpers `change` and `build`, and the step checker and board generator that 0113 wrote as evidence, moved into `scripts/changes/` as the `board` helper.
  - The rewrite of `spec-driven-development.md` around `changes/`.
  - The retirement of the proposal pipeline: its workflows, skills, tools, agent, tests, and plan file, with their rows in the residual registers.
- **Out of scope.**
  - `digest`, `where`, and `since`, which read the specification structure, come with 0115. Until then a digest is written by hand, as 0113's are.
  - `triage` and `issue` come when the first area's findings migrate to `issues/`.
  - The `lookup` tool and its MCP server stay: they search the specification and are not part of the pipeline.
  - `proposals/` stays as written.

## Terms

| Term | Meaning |
| :-- | :-- |
| Helper | A skill or workflow under `.claude/` that any activity of the [workflow](../README.md#workflow) can use. The changes README lists the helpers. |
| Generic helper | A helper that takes its target and its conventions as arguments, so it works on any document, diff, or branch. |
| Bound helper | A helper that reads the layouts of `changes/`, `issues/`, or the specification. A change of those layouts touches the bound helpers alone. |
| Charter | A short instruction that gives one reviewer of `review` its point of view, such as "check every claim about the tree against the tree". |
| Skill | A `SKILL.md` under `.claude/skills/<name>/` that loads instructions into the session that invokes it. |
| Workflow | A script under `.claude/workflows/` that runs several agents in a fixed order and returns their combined result. |

## High-level design

Each helper does one job and stops. None loops until its findings run out; the owner decides when a change has converged and runs a helper again when needed. `review` is the only helper that runs several agents at once, so it is a workflow, and the others are skills. The logic of the bound helpers that needs no model, such as numbering a change, checking step entries, generating the board, and rewriting links when a folder moves, lives in Python under `scripts/changes/`, so it is tested without a model and the skills call it.

The proposal pipeline is deleted in the same change. Git history keeps it, the open proposals are set aside, and nothing in flight depends on it.

## Detailed design

### `review`

A workflow, `.claude/workflows/review.js`.

- **Arguments.** The target: paths, a diff range, or a branch. Optional charters, which replace the defaults. `--since <rev>` limits the review to what changed since that commit. `--fix` applies confirmed findings that need no decision. `--record <file>` appends a summary entry to that file, such as a change's `review-history.md`.
- **Reviewers.** One agent per charter, in parallel. The default charters are a tree checker (every claim about the code, the specification, or the tree is checked against it), an implementer (what is ambiguous, contradictory, or would fail when built), and a documentation reader (each edited documentation page is checked against the specification, the code, and `doc-content.md`). A security reviewer runs on request. Each finding states the location, the claim, the evidence, a severity, a proposed fix, and whether the fix needs a decision.
- **Verifier.** One agent per finding confirms or refutes it from the tree, without the reviewer's reasoning. A refuted finding is dropped.
- **Fixes.** With `--fix`, one agent applies the confirmed findings that need no decision. The others are returned as open decisions or issues for the caller to place.
- **One round.** The workflow runs once and returns its report. It does not rerun itself.

### `tidy`

A skill. It brings one document into a template given as an argument (a file, or the template of `changes/README.md` by default): section order, terms first, each statement made once, history moved to the file the caller names, and `doc-style.md` applied. A verifier agent compares the result with the original and refuses the edit if any decision, requirement, value, or dependency changes meaning.

### `fact-check`

A skill. It lists each factual claim of a document about the tree, the specification, or an external source, checks each one, and reports the claims it cannot confirm with the evidence it found. It edits nothing.

### `ask`

A skill. It turns the open questions in a document into multiple-choice questions with the recommendation first, asks the owner, and records each answer with its date in the section the caller names. In a change folder it moves the settled question from "Open decisions" to "Decisions" in `decisions.md`.

### `change`

A skill over `scripts/changes/change.py`.

- `new <slug>` creates the next numbered folder with `change.md`, `decisions.md`, `steps.md`, and `review-history.md` from the template.
- `status <number> <status>` sets a change's status, and `step <step> <status>` sets a step's status. Each checks that the new status agrees with the steps, as the changes README states.
- `archive <number>` moves an `Implemented` or `Cancelled` change to `archive/<date>-<number>-<slug>/` and rewrites every link into it, with the same link rewriting 0113 used.
- `check` and `board` run the step checker and the board generator.

### `build`

A skill. It carries out one step, or several that share a branch, on a branch named `change/<step>-<slug>`: the specification edits first, then the code and the tests, iterating with `lenny-test --changed`, then the documentation pages the change names and any page the documentation check flags. It sets the step to `Building`, writes the digest under `digests/` (by hand until 0115's generator), and stops on a choice that neither the specification nor `decisions.md` settles, recording it as an open decision. Landing is the owner's act: when the branch merges, `change step <step> Landed` records it.

### `spec-driven-development.md`

The rule keeps its principle: the specification is the source of truth, code implements it, and tests verify it. The sections on the proposal pipeline, a proposal as a directory, and the edit rule for landed proposals are replaced by a pointer to `changes/README.md`: a change that alters a requirement, a defined value, or an operation goes through a change folder, and its specification edits land on the step's branch before the code that depends on them. The rule keeps the paragraph that a landed record is not edited, applied to `changes/archive/` and to `proposals/`.

## Current state

- `.claude/workflows/`: `change-proposal.js`, `change-proposal-decisions.js`, `mechanism-table-converge.js`, `implement-proposal.js`, `implement-proposal-build.js`, `proposal-conformance.js`, `build-gaps-spec-unblock.js`, `close-test-gaps.js`, `migrate-proposal.js`, `fix-line-citations.js`, and `fidelity-review.js`.
- `.claude/skills/`: `change-proposal`, `implement-proposal`, and `close-test-gaps`.
- `.claude/tools/`: `proposal-status.mjs`, `cp-state.mjs`, `cp-round-boundary.sh`, `spec-lease.mjs`, `check-proposal-split.mjs`, `classify-diff.mjs`, and `register-row.mjs` serve the pipeline. `lookup.mjs`, `lookup-mcp.mjs`, and `lookup-embed.py` serve the `lookup` MCP server in `.mcp.json`.
- `.claude/agents/cp-writer.md`, `.claude/proposal-pipeline-rework-plan.md` (2,478 lines), and `.claude/tests/` (26 files, of which `lookup.test.mjs` and `lint-workflows.mjs` are not specific to the pipeline).
- `tests/registers/residual-change-graph-coverage.yaml` lists 45 paths under `.claude/`.
- `changes/0113-records-and-workflow/evidence/steps.py` and `restructure.py` hold the step checker, the board generator, and the link rewriter.

## Specification and non-specification changes

No specification text changes.

- New: `.claude/workflows/review.js`; `.claude/skills/{tidy,fact-check,ask,change,build}/SKILL.md`; `scripts/changes/` with the step checker, the board generator, the change commands, the link rewriter, and their tests.
- Edited: `.claude/rules/spec-driven-development.md`; `changes/README.md`, where the helpers table names each helper's file; `AGENTS.md` if it names a retired helper; `tests/registers/residual-change-graph-coverage.yaml`.
- Removed: the pipeline's workflows, skills, tools, agent, plan file, and tests listed under "Current state".

## Documentation

No reader-facing behavior changes. The helpers are contributor tooling, and `changes/README.md` describes them.

## Tests

- `scripts/changes/`: Go-free Python tests for numbering, status checks, the step checker (format, identifiers, cycles, and partial-dependency warnings), the board, archiving with link rewriting, and the link checker, run by tier 0.
- `.claude/tests/lint-workflows.mjs` covers `review.js`.
- Tier 0 passes with the pipeline's files and register rows removed.

## Validation owed

See `steps.md`.
