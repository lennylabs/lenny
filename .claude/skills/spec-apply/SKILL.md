---
name: spec-apply
description: Apply an approved spec change proposal's staged edits to spec/, then verify every applied edit aligns exactly with the proposal and fix discrepancies until a verification round is clean. Use when a proposal under proposals/ is signed off and its spec edits should land. Applies spec/ targets only; staged code, chart, and docs changes remain with the implementation work.
argument-hint: <path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Spec apply

This skill takes an approved proposal under `proposals/` and lands its staged spec edits in `spec/`. The workflow applies the edits, verifies that every applied edit aligns exactly with the proposal's staged text, fixes any discrepancy, and re-verifies until a full verification round reports none.

## Hard constraints

- The run edits only the `spec/` files that the proposal's "Proposed spec changes" section names as targets, plus exactly one line of the proposal on successful completion: the Status bullet, set to "Applied to spec (<date>)." after a clean, blocker-free application. `docs/`, `pkg/`, `cmd/`, `charts/`, and `schemas/` are never modified, and no other part of the proposal is touched. Staged changes whose targets are outside `spec/` are reported as remaining work, never applied.
- The proposal must record approval in its Status bullet. Without recorded approval, or with a status already recording "Applied to spec", the run stops before any edit.
- `spec/` must be clean in git before the run starts, so `git diff -- spec/` is exactly the applied change set and verification can compare it against the proposal.
- Anchors are located by the quoted text and section headings in the proposal's anchor instructions. Line numbers in anchor instructions are location hints that drift; an edit whose anchor text cannot be found is recorded as unappliable rather than guessed.

## Spec content rules

These rules take precedence over verbatim application. Every deviation they force is recorded and reported; the verifier treats recorded deviations as expected differences.

- The spec never references source code files or implementation paths (`pkg/`, `cmd/`, `charts/`, `sdks/`, `tests/`, `migrations/`, `.go` or other source files). Staged text carrying such a reference is rephrased into behavioral spec language or the reference is dropped.
- The spec cross-references other spec content by section number only: `§X.Y` or a relative markdown link to a section anchor. A line-number cross-reference in staged text is replaced with the containing section's number.
- `.claude/rules/doc-style.md` governs spec prose. Staged text is applied as written (the proposal pipeline already styled it); the applier does not restyle it.

## Procedure

### Step 1: Preconditions (inline, before the workflow)

1. Resolve the proposal path from the arguments and read the proposal.
2. Confirm the Status bullet records approval (for example "Approved for implementation"). If it records a draft or in-review state, stop and report; do not invoke the workflow.
3. Run `git status --porcelain -- spec/`. If any `spec/` file is dirty, stop and report; the verification model requires a clean baseline.
4. Compute `repoRoot` (the absolute repository root) and `date` (today's date as `YYYY-MM-DD`; workflow scripts cannot call Date).

### Step 2: Run the workflow

The workflow script lives at `.claude/workflows/spec-apply.js` and is invoked by name. Call `Workflow({name: "spec-apply", args: …})` with:

```json
{
  "proposalPath": "proposals/<file>.md",
  "repoRoot": "<absolute repo root>",
  "date": "<YYYY-MM-DD>",
  "maxRounds": 5
}
```

Pass `args` as a JSON object value. Agents inherit the session model and effort level.

### Step 3: Interruptions

On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Completed agents replay from the journal cache; because appliers edit files, confirm after resume that the final verification round ran against the final file state (the loop re-verifies all files every round, so a completed run guarantees this).

### Step 4: Report

1. Run `git status --porcelain` and confirm the only modified files are `spec/` targets named by the proposal. If anything else changed, restore it and report the violation.
2. On `status: "applied"`: report the edits applied per file, the recorded rule deviations, the verification rounds run with the discrepancy counts, and the staged non-spec changes that remain for the implementation work. The workflow has set the proposal's Status bullet to "Applied to spec (<date>)."; suggest committing, but do not commit unless the user asks.
3. On `status: "applied-with-blockers"`: as above, plus the unappliable edits with their reasons (usually drifted anchors). The Status bullet is left unchanged because the application is partial; the user resolves the blockers, and the report states plainly that the spec holds a partial application.
4. On `status: "not-clean"`: the round cap was hit with discrepancies outstanding. Report them with their expected and observed text for a human decision.
5. On `status: "not-approved"` or `status: "dirty-baseline"`: no file changed; report the reason.

## Maintenance

The workflow script is canonical at `.claude/workflows/spec-apply.js`; this file carries the procedure and rules only. When application surfaces a new discrepancy class (a way applied text can diverge from staged text, or a new forbidden reference pattern), add it to the verifier checklist or the rules sweep in the script, and mirror any content-rule change in the "Spec content rules" section here. Keep the content rules short and mechanical; the verifiers quote them verbatim.
