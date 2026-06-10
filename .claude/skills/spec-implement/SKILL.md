---
name: spec-implement
description: Implement an approved spec proposal in code. Verifies the proposal's spec edits are already applied to spec/, plans the code blast radius and an ordered build sequence, implements it one step at a time with tests run to green, then closes the BUILD-GAPS.md findings that reference the proposal. Use after a proposal is approved and its spec edits are landed (via spec-apply); it implements the code, not the spec.
argument-hint: <path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Spec implement

This skill takes an approved spec proposal whose spec edits are already applied and implements the spec change in code. The implementation is the `spec-implement-build` subworkflow: it identifies the blast radius, plans an ordered build sequence, and implements the sequence one step at a time with tests created and run along the way. After a green implementation, the skill closes the BUILD-GAPS.md findings that reference the proposal.

It is the third stage of the proposal pipeline: `spec-proposal` writes and converges a proposal, a human approves it, `spec-apply` lands its spec edits in `spec/`, and `spec-implement` implements the code and closes the findings.

## Hard constraints

- The skill does not modify `spec/`. The spec edits are applied by `spec-apply` before this skill runs; this skill verifies they are present and implements the code against them. The guard hook blocks direct spec writes once the proposal reads "Applied to spec".
- The skill verifies the precondition; it does not apply the spec. If the proposal's Status is not "Applied to spec", or the staged spec edits are not actually present in `spec/`, the run stops and reports that `spec-apply` must run first.
- Code changes follow `.claude/rules/code-best-practices.md` and `.claude/rules/test-coverage.md`: tests across every tier the change reaches (not unit alone), run to green, with the `// spec:` and `// diagnosis:` conventions.
- The skill closes findings only after the implementation verifies green across the reached tiers. It never opens or re-opens a finding. A proposal with no referencing finding still implements; the close step is a no-op.

## The subworkflow

`spec-implement-build` is the implementation engine, stored separately so it is reusable and matches the two-stage structure:

1. **Plan** — a planner reads the proposal in full (the spec edits are landed) and greps `spec/`, `pkg/`, `cmd/`, `charts/`, `schemas/`, and `migrations/` for every surface the change touches, producing the blast radius and an ordered build sequence. Each step names its work, target files, dependencies on earlier steps, the test tiers it must create and run, and the spec sections it implements. A completeness critic checks the plan covers the whole proposal and the sequencing puts prerequisites first; the plan is revised once if it finds gaps.
2. **Build** — each step is implemented in order (sequential, because later steps depend on earlier ones and share the working tree). For each step the implementer writes the code, creates or modifies the step's tests across its tiers, runs tier 0 and tier 1 plus each higher tier the step reaches, fixes until green, and commits the step.
3. **Verify** — a final pass runs the reached tiers across the whole change with `lenny-test --changed --max-tier <tier>` and reports changed-line coverage; one fix pass runs if it finds failures.

## Procedure

### Step 1: Preconditions (inline, before the workflow)

1. Resolve the proposal path from the arguments and read its Status bullet. If it does not begin "Applied to spec", stop: the spec edits are not landed. Run the `spec-apply` skill on the proposal first (which itself requires an approved proposal), then re-run this skill. Do not invoke the workflow.
2. Run `git status --porcelain`. A clean or nearly clean tree is preferred so the implementation commits and the `lenny-test coverage --diff` baseline are clean; note any pre-existing changes.
3. Compute `repoRoot` (the absolute repository root) and `date` (today's `YYYY-MM-DD`; workflow scripts cannot call Date).

### Step 2: Run the workflow

The script lives at `.claude/workflows/spec-implement.js` (and its subworkflow at `.claude/workflows/spec-implement-build.js`), invoked by name:

```json
{
  "proposalPath": "proposals/<file>.md",
  "repoRoot": "<absolute repo root>",
  "date": "<YYYY-MM-DD>",
  "maxTier": "e2e"
}
```

`maxTier` is optional; omit it to let each step and the final verify run every tier the change reaches. Agents inherit the session model and effort level; run this skill with a strong model at high effort.

### Step 3: Interruptions

On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Because the build steps commit per step, a resumed run re-plans against the current tree; confirm the plan accounts for steps already committed before letting it re-run them.

### Step 4: Report

1. Run `git status --porcelain` and `git log --oneline` for the run's commits. Confirm the changes are under `pkg/`, `cmd/`, `charts/`, `schemas/`, `tests/`, and (for closed findings) `BUILD-GAPS.md`, and that `spec/` is unchanged by this run.
2. On `status: "implemented"`: report the blast radius, the build steps with their commits, the final green status and changed-line coverage, and the findings closed. Suggest pushing; do not push unless the user asks.
3. On `status: "implemented-not-green"`: report which tiers failed and the steps involved; the findings are left OPEN for review. The implementation commits remain on the branch.
4. On `status: "not-applied"`: no code changed; report that `spec-apply` must run first and why (status not "Applied to spec", or the staged edits are missing from `spec/`).
5. Do not push or open a PR unless the user asks.

## Relationship to the build loop

`close-build-gaps.sh --mode proposals` (driven by `build-gaps-spec-unblock`) does the same apply-then-implement-then-close work in a finding-driven `claude -p` batch loop across many proposals. `spec-implement` is the structured, single-proposal, workflow-based path: it plans an explicit build sequence and implements it step by step under subagent orchestration. Use `spec-implement` for one proposal with a large or ordering-sensitive blast radius; use the build loop to drain many approved-proposal findings autonomously.

## Maintenance

The workflow scripts are canonical at `.claude/workflows/spec-implement.js` and `.claude/workflows/spec-implement-build.js`; this file carries the procedure and rationale only. Keep the subworkflow description here in sync with the script. When the implementation surfaces a recurring planning or sequencing gap, strengthen the completeness-critic prompt in the subworkflow rather than restating it here.
