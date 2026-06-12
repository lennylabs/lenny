---
name: spec-proposal
description: Write an adversarially validated spec change proposal under proposals/ from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec defect, contradiction, or gap, asks for a spec fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages spec edits for sign-off; it never modifies spec/ itself.
argument-hint: <problem statement | path to notes | path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Spec proposal writer and convergence loop

This skill produces a reviewed proposal document in `proposals/` and converges it against the spec and the code. It has two modes sharing one workflow:

- **new**: the input is a problem statement. The workflow validates the problem's premises, drafts a change set, adversarially challenges each change, writes the proposal file, and then enters the review loop.
- **review**: the input is the path of an existing proposal under `proposals/`. The workflow enters the review loop directly.

The review loop is shared: rounds of multi-lens adversarial review, two-skeptic verification of every finding, and fixes, repeated until two consecutive rounds confirm zero findings.

## Hard constraints

- The run creates or edits exactly one file: the proposal (the new file in new mode, the given file in review mode). Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages spec edits as fenced markdown blocks to apply after sign-off.
- All problem input is inline (the argument and the conversation). The skill reads no tracking documents; evidence comes from `spec/`, `schemas/`, `pkg/`, `cmd/`, `charts/`, and git history. Progress-tracking and audit prose elsewhere in the repository are leads to verify, never evidence.
- Prose follows `.claude/rules/doc-style.md`.
- New proposal file names are `NNNN_[new|fix]_<kebab-slug>.md`. `NNNN` is the next free zero-padded number among existing numbered proposals. `fix` is for proposals that correct or reconcile existing spec text (contradictions, wrong ownership assignments, unreachable features). `new` is for proposals that add a capability or component the spec does not describe.
- Review findings are real errors only: false citations, infeasible actor assignments, contradictions, missing edit sites, and broken mechanisms. Style preferences, optional improvements, and hypothetical hardening are excluded by construction (the materiality skeptic refuses them); conventions are handled by a dedicated one-shot pass outside the error loop.

## Proposal format conventions

The writer and reviewer agents receive these conventions. They are derived from the existing files in `proposals/`; when those files and this list disagree, the existing files win.

- Title line: `# Proposal: <title>`.
- Header bullets: `**Status:**`, `**Date:**`, `**Scope:**` (one- or two-sentence summary naming the finding IDs from the inline input when applicable).
- Status lifecycle: the writer creates the proposal with "Draft for review."; the workflow replaces the state with "Verified (<date>). Converged after N adversarial review rounds (M findings fixed); awaiting sign-off." when the review loop converges, and leaves it untouched otherwise. Sign-off (an approved state) is recorded by a human, or by the `build-gaps-spec-unblock` pipeline when it resolves a converged proposal's open decisions; `implement-proposal` acts only on approved proposals.
- Staging boilerplate paragraph: "This document stages the proposed spec … edits. It does not modify any spec file. Apply the changes in the … section after sign-off."
- Numbered sections in this order, omitting the ones with no content: Problem; Decisions; Design overview; Detailed design; Observability surface; CRD and RBAC changes; Proposed spec changes; Non-goals; Testing; Findings closed on application; Resolved in adversarial review; Open decisions for review; Files touched on application.
- The Problem section cites spec text as `spec/<file>.md:<line>` or `§X.Y` relative links, cites code as `pkg/...:<line>`, and names the finding IDs the proposal unblocks, if the inline input provided any.
- The Proposed spec changes section has one subsection per target file and section, each with an anchor instruction ("Append after …", "Replace the row …") and a fenced block containing the exact text to insert, written so it can be applied mechanically after sign-off.
- Non-goals records the alternatives that were considered and dropped, with the reason.
- Files touched on application is consistent with the Proposed spec changes section.

## Why the review loop is built this way

These design points come from convergence runs on prior proposals in this repository. Keep them when editing the script.

- **Two consecutive clean rounds, not one.** Fix rounds introduce their own errors: fixers add predicate text that drifts from the design's invariants, and clean rounds have been followed by rounds with confirmed findings in fixer-written text. A single clean round demonstrates nothing about the text the previous fixer wrote.
- **Two skeptics per finding, both must confirm.** One re-derives the evidence from the files; one judges materiality assuming the evidence is true, with instructions to default to refuted. The split kills plausible-but-wrong findings and nitpicks separately.
- **Refuted findings are remembered and injected into later rounds.** Without the memory, a refuted finding resurfaces in a later round, wastes verification, and can block convergence.
- **Dedup before verification.** Independent lenses converge on the same root error under different phrasings; verifying duplicates multiplies cost for nothing.
- **Security, Kubernetes, and performance are always-on fixed lenses.** Every round runs the four structural lenses (citations, feasibility, edit-sites, mechanism) plus three non-functional lenses that must be present every round: `security` (control regression AND the trust boundary / durability of any security-bounding value), `kubernetes` (controller-runtime and API-convention soundness), and `performance` (top-tier write rates, bottlenecks, and failure-mode reliability under store outage and coordinator handoff). These were promoted to always-on after dedicated security and reliability passes caught a showstopper (a residual-state security bound sourced from an untrusted pod self-report with no durable fallback) that the rotating-lens loop had converged past. Do not demote them to rotating.
- **A rotating extra lens, on top of the fixed set.** The fixed lenses develop shared blind spots over rounds because they re-read the same document. One extra lens rotates per round (operational consistency, then fresh holistic read) and has found confirmed errors in rounds the fixed lenses passed.

## Error classes with a record of surviving verification

The lens prompts in the script enumerate these. They are the classes that have produced confirmed findings; extend the list when a new class surfaces.

- A named actor that does not exist, or an actor assigned a write it cannot perform under the spec/04 §4.6.3 CRD field-ownership table and its RBAC paragraph.
- A check placed at a layer that cannot see the data it needs (an in-process admission webhook resolving a cross-resource reference; a gateway surface evaluating a CRD-only field).
- A data-flow direction asserted backwards (which side of a store-CRD mirror is authoritative; verify in the reconciler code, never from memory).
- A mandatory gate that one write path bypasses (a field settable through a surface the gate never inspects, such as a directly applied custom resource).
- Build-phase ordering violations: a `spec/18` deliverable depending on artifacts a later phase introduces.
- Edits to generated artifacts instead of their authoring source (alert rules are authored in `pkg/alerting/rules` and rendered by `make generate`; check file headers for generation notes).
- Revert and rollout races: a status condition that clears while pods rendered under the old configuration still run.
- Predicate drift: a trigger condition stated with different conjuncts in different sections of the proposal (design section, summary table, constant comment, proposed spec text, tests).
- Missing companion edit sites: a metric without its `spec/16` inventory row and `docs/reference/metrics.md` row; an alert without its `docs/runbooks/` page (`tests/tier11_docs` enforces slug resolution); a classification table without its companion prose list; an operator-guide sentence describing superseded behavior.
- A field defined in one spec section cross-referenced as living in another.
- An alert defined on a metric that no spec-defined evaluation surface collects.

## Procedure

### Step 1: Determine the mode and assemble inputs (inline, before the workflow)

1. If the argument is the path of an existing file under `proposals/`, the mode is **review**. Otherwise the mode is **new** and the argument (plus the conversation) is the problem statement.
2. Common inputs:
   - `date`: today's date as `YYYY-MM-DD` (workflow scripts cannot call Date).
   - `repoRoot`: the absolute repository root.
   - `exemplar`: the path of the highest-numbered existing proposal (in review mode, excluding the proposal under review).
   - `maxReviewRounds`: default 12.
3. New mode only:
   - Read the spec sections and code paths the problem names so the dossier carries concrete citations rather than paraphrase.
   - `problem`: a problem dossier of one to three paragraphs stating the problem.
   - `context`: a block listing every citation gathered so far (spec file:line, code file:line, finding IDs from the inline input, prior conversation conclusions). Distinguish established facts from unverified claims; the workflow re-verifies both.
   - `nextNumber`: list `proposals/`, take the highest `NNNN_` prefix among numbered files, add one, zero-pad to four digits. Ignore unnumbered files.
4. Review mode only:
   - `proposalPath`: the path of the proposal under review, relative to the repository root.
   - `context`: a short list of the spec sections and code packages the proposal touches, with approximate line anchors, gathered by grepping for the proposal's main identifiers. This focuses reviewers; they re-verify everything themselves.

### Step 2: Run the workflow

The workflow script lives at `.claude/workflows/spec-proposal.js` and is invoked by name. Call `Workflow({name: "spec-proposal", args: …})` with the mode-appropriate args:

```json
{
  "mode": "new",
  "problem": "<the problem dossier>",
  "context": "<citations and prior conclusions>",
  "date": "<YYYY-MM-DD>",
  "nextNumber": "<NNNN>",
  "exemplar": "proposals/<highest-numbered proposal>.md",
  "repoRoot": "<absolute repo root>",
  "maxReviewRounds": 12
}
```

```json
{
  "mode": "review",
  "proposalPath": "proposals/<file>.md",
  "context": "<assembled notes, or empty>",
  "date": "<YYYY-MM-DD>",
  "exemplar": "proposals/<highest-numbered other proposal>.md",
  "repoRoot": "<absolute repo root>",
  "maxReviewRounds": 12
}
```

Pass `args` as a JSON object value in the tool call. The script tolerates a JSON-encoded object string by parsing it; anything else aborts on the args guard.

Agents inherit the session model and effort level. Run this skill with the strongest available model at high effort; reviewer quality determines whether the loop converges on truth or on exhaustion.

### Step 3: Interruptions and non-convergence

- On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Completed agents replay from the journal cache and the run continues live from the cut point.
- On hitting `maxReviewRounds` without two consecutive clean rounds: inspect the trajectory in the returned `review.history`. If the confirmed-finding counts are decreasing and the last round was clean, raise `maxReviewRounds` in the persisted script file's default or pass a larger value, and resume with `{scriptPath, resumeFromRunId}`; the edit does not invalidate the cached prefix. If counts are flat or oscillating, stop and report the recurring findings for a human decision instead of burning rounds.

### Step 4: Report

1. Run `git status --porcelain` and confirm the only created or modified file is the proposal. If anything under `spec/` or any other path changed, restore it and report the violation.
2. On `status: "written"` or `status: "reviewed"`: read the proposal, then report the file path, the title, the refuted premises and dropped changes with reasons (new mode), whether the loop converged, the rounds run, the findings fixed per round with their titles, and the findings the skeptics refuted. On convergence the workflow has set the proposal's Status bullet to "Verified (<date>) …; awaiting sign-off."; state that the next step is sign-off, which records an approved state, after which `implement-proposal` lands the staged edits in `spec/` and implements the code. Without convergence the Status bullet is unchanged; say so.
3. On `status: "not-viable"` or `status: "no-change-needed"`: no file is written. Report the refuting evidence so the user can correct or withdraw the problem statement.
4. Do not apply any staged edit to `spec/`, and do not commit, unless the user asks.

## Maintenance

The workflow script is canonical at `.claude/workflows/spec-proposal.js`; this file carries the procedure, conventions, and rationale only. Other workflows invoke the script by name (`workflow("spec-proposal", args)`; the `build-gaps-spec-unblock` workflow does), so script edits must keep the args contract stable. When a convergence run surfaces a confirmed error class this file does not list, add it to the error-class list here and, when it fits an existing lens, to that lens's prompt in the script. Keep the finding bar's DO-NOT-report list intact; it is what keeps the loop from converging on nitpicks. When the proposal format conventions and the existing files in `proposals/` disagree, the existing files win; update the conventions list here.
