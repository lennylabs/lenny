---
name: implement-proposal
description: Implement an approved spec proposal end to end. Applies the proposal's staged spec edits to spec/ and verifies them, then (by default) implements the spec change in code via the build subworkflow and closes the BUILD-GAPS.md findings that reference it. Pass "spec-only" to land and verify the spec without touching code. Use after a proposal is approved.
argument-hint: <path to proposals/*.md> [spec-only]
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Implement proposal

This skill takes an approved spec proposal and carries it through to implementation. It applies the staged spec edits to `spec/` and verifies exact alignment, then implements the spec change in code through the `implement-proposal-build` subworkflow (blast radius, ordered build sequence, step-by-step implementation with tests), and closes the findings that reference the proposal. The spec always lands and is verified before any code.

It is the implementation stage of the proposal pipeline: `spec-proposal` writes and converges a proposal, a human approves it, and `implement-proposal` lands the spec and implements the code. It unifies the former `spec-apply` (land and verify spec) and `spec-implement` (plan, build code, close findings) into one entry point.

## Modes

- **Full (default)** — apply and verify the spec, then implement the code and close the findings. Invoke with the proposal path alone.
- **Apply-only** — apply and verify the spec, commit it, and stop. Invoke with `spec-only` after the path. This is the former `spec-apply` behavior; use it when the code is implemented elsewhere (for example the `close-build-gaps.sh --mode proposals` loop, which lands the spec this way and then implements the code itself).

## Hard constraints

- Spec first. The staged spec edits are applied and verified, and committed as their own commit, before any code is written.
- Spec edits are applied only through this skill's verified apply loop, never by hand. The guard hook blocks direct `spec/` writes unless an approved proposal is pending; once the proposal reads "Applied to spec" the hook blocks spec writes again, so the code phase cannot touch `spec/`.
- Spec content rules hold over verbatim application: the spec never references source code paths, and cross-references other spec content by section number, never line number. The apply loop rephrases or drops staged text that would violate these and records the deviation.
- Code changes follow `.claude/rules/spec-driven-development.md`, `.claude/rules/code-best-practices.md`, and `.claude/rules/test-coverage.md`: every behavior traces to a spec section, tests run across every tier the change reaches, and they pass.
- Findings close only after the implementation verifies green. The skill never opens or re-opens a finding. A proposal with no referencing finding still implements; the close step is a no-op.

## Procedure

### Step 1: Preconditions (inline, before the workflow)

1. Resolve the proposal path and the optional `spec-only` modifier from the arguments. Read the proposal's Status bullet. It must begin "Approved" (or "Applied to spec" for an idempotent re-run). A "Draft" or "Verified" status is not approved: stop and report that it needs sign-off.
2. Run `git status --porcelain -- spec/`. The apply verification diffs the working tree against a clean `spec/` baseline, so `spec/` must be clean. If it is dirty, stop and report.
3. Compute `repoRoot` (the absolute repository root) and `date` (today's `YYYY-MM-DD`; workflow scripts cannot call Date).

### Step 2: Run the workflow

The script lives at `.claude/workflows/implement-proposal.js` (subworkflow at `.claude/workflows/implement-proposal-build.js`), invoked by name:

```json
{
  "proposalPath": "proposals/<file>.md",
  "repoRoot": "<absolute repo root>",
  "date": "<YYYY-MM-DD>",
  "implementCode": true
}
```

Set `implementCode` to `false` for spec-only; every tier each step and the final verify reach is run. Agents inherit the session model and effort level; run this skill with a strong model at high effort.

### Step 3: Interruptions

On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. The spec apply commits before the code phase and the build steps commit per step, so a resumed run re-plans against the current tree; confirm the plan accounts for work already committed before letting it re-run.

### Step 4: Report

1. Run `git status --porcelain` and `git log --oneline` for the run's commits. Confirm the spec landed as its own commit, the code changes are under `pkg/`, `cmd/`, `charts/`, `schemas/`, and `tests/`, and `BUILD-GAPS.md` carries the closed findings.
2. On `status: "implemented"`: report the spec commit, the blast radius, the build steps with their commits, the final green status, the changed-line coverage, that the design-conformance review came back clean, and the findings closed. Suggest pushing; do not push unless asked.
3. On `status: "spec-only"`: the spec is landed, verified, and committed; report it and the findings left for the code stage.
4. On `status: "implemented-not-green"`: report which tiers failed, whether coverage fell below the floor, and any unresolved design-conformance findings (`reviewFindings`); the findings are left OPEN and the commits remain on the branch. The `resumeNote` says how to continue.
5. On `status: "build-step-stuck"`: a build step stayed red after `maxStepAttempts`, so the sequence aborted before its dependents. Report the `stuckStep`, the spec and partial code commits on the branch, and the `resumeNote`; the findings are left OPEN.
6. On `status: "not-approved"`, `"spec-not-clean"`, `"spec-applied-with-blockers"`, or `"not-aligned"`: report the reason. `spec-not-clean` and `spec-applied-with-blockers` leave the spec edits in the working tree for inspection; `not-aligned` means a re-run found the spec drifted from the proposal.
7. Do not push or open a PR unless the user asks.

## The subworkflow

`implement-proposal-build` is the code-implementation engine, stored separately so it is reusable and matches the plan-then-build structure:

1. **Plan** — a planner reads the proposal in full (spec edits already landed) and greps the codebase for every surface the change touches, producing the blast radius and an ordered build sequence. The blast radius and sequence cover the surfaces the proposal **removes** as well as those it adds: every eliminated mode, field, RPC, frame, metric, enum value, or file gets an explicit removal step that also deletes the code, tests, and fixtures orphaned by it, so no removed surface is left compiling-but-dead. A completeness critic checks the plan covers the whole proposal (including the removals), and sequences prerequisites first; the plan revises once if it finds gaps.
2. **Build** — each step is implemented in order (sequential, because later steps depend on earlier ones and share the working tree). Each step is **gated before the sequence advances** by an inner loop: an implementer writes the code and tests, an **independent agent verifies** the step's tiers are green (the implementer's self-report is advisory), and an **adversarial design-conformance review** reads the step's own diff against the proposal through two lenses (design conformance, and named invariants and edge cases) scoped to that step — a surface this step adds for a later step to consume is out of scope. The step advances only when it is both green and review-clean; otherwise the loop fixes and re-checks, bounded by `maxStepAttempts` (default 50). Catching a divergence at the step that introduced it is cheaper than catching it in the final review and keeps a wrong foundation from propagating. If a step is still red or divergent after the cap, the sequence **aborts** rather than building dependent steps on it: the subworkflow returns `status: "build-step-stuck"` with the `stuckStep`, the reason, and a `resumeNote`, and the spec and completed steps stay committed. The plan is a prediction made before any code exists, so the Build phase re-checks it as reality lands: every `replanEvery` completed steps (default 4) and after any step that struggled (took at least `replanStruggleAttempts` attempts), a read-only critic checks whether the **remaining** plan still matches what was built — an unplanned surface that got touched, a removal that orphaned more than foreseen, a step now redundant or mis-sequenced. On evidenced drift it re-plans the remaining steps **forward-only**: completed steps are immutable, only the not-yet-built tail is replaced, bounded by `maxReplans` (default 6). This is distinct from the per-step review, which judges completed work rather than the correctness of the plan ahead.
3. **Verify** — a final pass runs the reached tiers across the whole change with `lenny-test --changed`, reports changed-line coverage, and runs a **dead-code sweep** (grep for every removed identifier, mode, field, RPC, frame, metric, and enum value and confirm none survives as a live reference; a surviving removed surface or orphaned caller is a failure). It also applies a **coverage gate**: `green` requires every reached tier to pass *and* changed-line coverage at the floor (`coverageFloor`, default 80%), with a behavior-preserving refactor exempt; below-floor coverage is a failure the fix loop closes by adding tests. It iterates fix-and-re-run until green, the sweep is clean, and coverage meets the floor, bounded by `maxVerifyRounds` (default 25, with an early exit when a non-green result lists no actionable failures). The standing dead-code rule is in the build subworkflow's injected rules and applies to every step.
4. **Review** — each step was already reviewed against its own diff during Build; this final pass reads the **whole change** against the pre-implementation baseline to catch what a step-scoped review cannot — cross-step interactions, a surface one step added and another was meant to consume, and whole-change completeness — through three lenses (design conformance, named invariants and edge cases, and blast-radius completeness). A fix round applies the findings and the loop re-reviews until clean, bounded by `maxReviewRounds` (default 50); a non-trivial review re-confirms green afterward. Findings close only when the build is green **and** the review is clean (`reviewClean`); otherwise the run returns `implemented-not-green` with the unresolved `reviewFindings`.

## Relationship to the build loop

`close-build-gaps.sh --mode proposals` (driven by `build-gaps-spec-unblock`) drains many approved-proposal findings autonomously in a `claude -p` batch loop; it lands each proposal's spec by invoking this skill in spec-only mode, then implements the code itself. Use `implement-proposal` directly for one proposal with a large or ordering-sensitive blast radius; use the build loop to drain many.

## Maintenance

The workflow scripts are canonical at `.claude/workflows/implement-proposal.js` and `.claude/workflows/implement-proposal-build.js`; this file carries the procedure and rationale only. Keep the subworkflow description here in sync with the script. When the implementation surfaces a recurring planning or sequencing gap, strengthen the completeness-critic prompt in the subworkflow.
