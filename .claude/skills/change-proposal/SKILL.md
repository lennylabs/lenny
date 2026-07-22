---
name: change-proposal
description: Write an adversarially validated change proposal under proposals/, staging spec edits and/or core-product or test-infrastructure code changes, from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec or implementation defect, contradiction, or gap, asks for a fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages its changes for sign-off; it never modifies spec/, pkg/, or docs/ itself.
argument-hint: <problem statement | path to notes | path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Change proposal writer and convergence loop

This skill produces a reviewed proposal document in `proposals/` and converges it against the spec and the code. A proposal may stage spec edits, core-product code changes, test-infrastructure changes, or a combination; the review loop validates whichever it stages. It has two modes sharing one workflow:

- **new**: the input is a problem statement. The workflow validates the problem's premises, drafts a change set, adversarially challenges each change, writes the proposal file, and then enters the review loop.
- **review**: the input is the path of an existing proposal under `proposals/`. The workflow enters the review loop directly.

The review loop is shared: rounds of multi-lens adversarial review, two-skeptic verification of every finding, and fixes, repeated until two consecutive rounds confirm zero findings.

## Hard constraints

- The run creates or edits exactly one file: the proposal (the new file in new mode, the given file in review mode). Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages its changes (spec edits and/or code or test changes) as fenced markdown blocks or precise change descriptions to apply after sign-off.
- All problem input is inline (the argument and the conversation). The skill reads no tracking documents; evidence comes from `spec/`, `schemas/`, `pkg/`, `cmd/`, `charts/`, and git history. Progress-tracking and audit prose elsewhere in the repository are leads to verify, never evidence.
- Prose follows `.claude/rules/doc-style.md`.
- New proposal file names are `NNNN_[new|fix]_<kebab-slug>.md`. `NNNN` is the next free zero-padded number among existing numbered proposals. `fix` is for proposals that correct or reconcile existing behavior — spec text, core-product code, or test infrastructure (contradictions, wrong ownership assignments, unreachable features, code-to-spec divergences). `new` is for proposals that add a capability or component the spec or implementation does not yet provide.
- Review findings are real errors only: false citations, infeasible actor assignments, contradictions, missing edit sites, broken mechanisms, and a Testing section that omits the tests the changed behavior requires. Style preferences, optional improvements, additional nice-to-have tests, and hypothetical hardening are excluded by construction (the materiality skeptic refuses them); conventions are handled by a dedicated one-shot pass outside the error loop.

## Proposal format conventions

The writer and reviewer agents receive these conventions. They are derived from the existing files in `proposals/`; when those files and this list disagree, the existing files win.

- Title line: `# Proposal: <title>`.
- Header bullets: `**Status:**`, `**Date:**`, `**Scope:**` (one- or two-sentence summary naming the finding IDs from the inline input when applicable).
- Status lifecycle: the writer creates the proposal with "Draft for review."; the workflow replaces the state with "Verified (<date>). Converged after N adversarial review rounds (M findings fixed); awaiting sign-off." when the review loop converges, and leaves it untouched otherwise. Sign-off (an approved state) is recorded by a human, or by the `build-gaps-spec-unblock` pipeline when it resolves a converged proposal's open decisions; `implement-proposal` acts only on approved proposals.
- Staging boilerplate paragraph: "This document stages the proposed … changes. It does not modify any spec, code, or doc file. Apply the changes in the … section after sign-off."
- Numbered sections in this order, omitting the ones with no content: Problem; Decisions; Design overview; Detailed design; Edge cases and accepted failure modes (every edge case and failure mode the design **accepts or defers**, not only the ones it changes — each as a row naming the observable outcome and the exact spec text and `docs/` page that states it, so an accepted or deferred case is documented where the reader or operator meets it rather than left only in the proposal's reasoning; a deferred *mechanism* still records its accepted behavior here and stages the sentence that documents it. Omit only when the change genuinely has no accepted or deferred failure mode); Observability surface; CRD and RBAC changes; Proposed changes (titled for the change type, e.g. "Proposed spec changes", with one subsection per target); Non-goals; Testing (the specific, insightful new tests to add during implementation — one per behavior the proposal changes, mapped to the tiers the change reaches and covering the non-happy-path, rather than a vague "add tests" note); Findings closed on application; Resolved in adversarial review; Open decisions for review; Files touched on application.
- The Problem section cites spec text as `spec/<file>.md:<line>` or `§X.Y` relative links, cites code as `pkg/...:<line>`, and names the finding IDs the proposal unblocks, if the inline input provided any.
- The Proposed changes section has one subsection per target (spec file and section, code package, or test file), each with an anchor instruction ("Append after …", "Replace the row …") and a fenced block containing the exact text to insert or a precise change description, written so it can be applied mechanically after sign-off.
- Non-goals records the alternatives that were considered and dropped, with the reason.
- Files touched on application is consistent with the Proposed changes section.

## Why the review loop is built this way

These design points come from convergence runs on prior proposals in this repository. Keep them when editing the script.

- **Two consecutive clean rounds, not one.** Fix rounds introduce their own errors: fixers add predicate text that drifts from the design's invariants, and clean rounds have been followed by rounds with confirmed findings in fixer-written text. A single clean round demonstrates nothing about the text the previous fixer wrote.
- **Two skeptics per finding, both must confirm.** One re-derives the evidence from the files; one judges materiality assuming the evidence is true, with instructions to default to refuted. The split kills plausible-but-wrong findings and nitpicks separately.
- **Refuted findings are remembered and injected into later rounds.** Without the memory, a refuted finding resurfaces in a later round, wastes verification, and can block convergence.
- **Dedup before verification.** Independent lenses converge on the same root error under different phrasings; verifying duplicates multiplies cost for nothing.
- **Security, Kubernetes, performance, reliability, client-facing surfaces, documentation alignment, and test coverage are always-on fixed lenses.** Every round runs the structural lenses (citations, feasibility, edit-sites, mechanism) plus the non-functional lenses that must be present every round: `security` (control regression AND the trust boundary / durability of any security-bounding value), `kubernetes` (controller-runtime and API-convention soundness), `performance` (top-tier write rates, bottlenecks, and the survival of bindings and counters under store outage and coordinator handoff), `reliability` (recovery-mechanism correctness under crash, restart, and store failover: idempotency of retried or replayed operations, dedup on at-least-once delivery, reclamation of orphaned pods, leases, and finalizers, bounded retry backoff, drain, and fail-open recovery onto reconciled state), `client-surface` (client-facing contract integrity), and `docs-alignment` (the docs/ tree reflects every changed behavior and never drives a spec or product decision). Security, Kubernetes, and performance were promoted to always-on after dedicated passes caught a showstopper (a residual-state security bound sourced from an untrusted pod self-report with no durable fallback) that the rotating-lens loop had converged past. Reliability is always-on for the same class of reason: the performance lens accounts for which state survives a failover but not whether the recovery mechanism is correct, and the Kubernetes lens covers the finalizer and level-triggered idioms but not idempotency of a retried operation, reclamation of an abandoned resource, or whether a fail-open path resumes on reconciled state, so a dedicated lens owns those. The `reliability` lens bounds against its neighbors: the performance lens keeps the capacity and state-survival math, the security lens keeps fail-closed on security paths and the trust boundary of security-bounding values, and the Kubernetes lens keeps the finalizer and level-triggered idioms. The `client-surface` lens verifies that a change to one client-facing representation (the REST API and its OpenAPI document, the MCP/A2A surfaces and `lenny/*` tool schemas, the wire proto and JSONL schemas, the adapter manifest, the per-language SDK types, the CRD schemas, client-visible enums and error codes, and the client-facing docs) is mirrored across every parallel representation, and that a name an external standard defines is not renamed while one client vocabulary is left half-renamed; the `edit-sites` lens checks internal edit completeness but not the parallel-client-representation and standard-vs-Lenny-defined boundary this lens owns. The `docs-alignment` lens verifies that every behavior the proposal changes is mirrored in the docs/ pages that describe it, under the rule that docs follow the spec and the implementation and are never used to decide what the spec or product should do; a doc-described scenario may seed a test case only after the doc is verified against the spec. This lens also owns two cases beyond mirroring a *changed* behavior, because an approved edge case is made of exactly the two categories that do not register as a change: (a) an edge case or failure mode the proposal **accepts or defers** whose outcome is stated only in the proposal's reasoning and appears in neither the staged spec text nor the `docs/` page that owns it — deferring the *mechanism* to a later proposal does not defer documenting the accepted behavior in the text that lands now; and (b) a **new operator-facing failure mode, or a new cause of an existing failure or data-loss path**, absent from the *narrative* operator docs (`docs/operator-guide/`, `docs/runbooks/`) that enumerate that failure's causes — distinct from the companion-row check the `edit-sites` lens owns (a metric's `metrics.md` row, an alert's runbook page), this is the failure narrative itself, which enumerates causes and must gain the new one. Cross-check the proposal's "Edge cases and accepted failure modes" section against the staged spec and doc edits: every row must resolve to landing text, not to reasoning alone. The `test-coverage` lens verifies the proposal lists the specific, insightful, relevant tests the changed behavior requires — one per changed behavior, mapped to the tiers the change reaches (per `test-coverage.md`) and covering the non-happy-path (empty, error, concurrent, boundary, spec-named-failure) — so a proposal that changes behavior without naming its tests, omits a tier the change plainly reaches, or lists only happy-path coverage where the change adds an error, concurrent, boundary, security or fail-closed, or spec-named-failure path is a finding; an additional nice-to-have test beyond that required coverage stays excluded. Test coverage was promoted to always-on because the loop otherwise refused every test-gap finding by construction, so a proposal could converge while listing no tests for the behavior it changed. Do not demote any of them to rotating.
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
- A recovery or retry mechanism that fails under the exact fault it handles: a retried or replayed operation that is not idempotent and lacks a dedup, fencing, or `deleted_at IS NULL` guard; an at-least-once delivery whose consumer has no dedup key; a failure path that abandons a pod, lease, sandbox, or finalizer with no reclaimer or GC; a retry without bounded jittered backoff that stampedes a recovering store; an outbound call with no timeout that stalls the path on one hung dependency; a drain or fail-open recovery that drops running work or resumes on stale, un-reconciled state.
- Predicate drift: a trigger condition stated with different conjuncts in different sections of the proposal (design section, summary table, constant comment, proposed spec text, tests).
- Missing companion edit sites: a metric without its `spec/16` inventory row and `docs/reference/metrics.md` row; an alert without its `docs/runbooks/` page (`tests/tier11_docs` enforces slug resolution); a classification table without its companion prose list; an operator-guide sentence describing superseded behavior.
- A client-facing contract changed in one representation but not its parallels: a REST field missing from `pkg/gateway/openapi/openapi.json`, the MCP tool schema, a language SDK, or the docs; a `schemas/lenny-adapter.proto` or JSONL change missing a language SDK or its tier-3 wire-contract test; a removed or renamed client-facing field still advertised by the served schema, an SDK, a CRD, or a doc; a standard-defined name (an MCP/A2A primitive) renamed, or one client vocabulary left half-renamed across the surface.
- A field defined in one spec section cross-referenced as living in another.
- An alert defined on a metric that no spec-defined evaluation surface collects.
- A behavior the proposal changes (a renamed or removed field, a changed default, a new error code, lifecycle step, metric, or alert) left undocumented or misdescribed in the `docs/` page that covers it: a `docs/` page describing superseded behavior, or an added metric or alert missing its `docs/reference/metrics.md` or `docs/runbooks/` companion. The fix is always a docs edit; docs follow the spec and never drive a spec or product change.
- An accepted or deferred failure mode or edge case named in the proposal's analysis (Problem, Detailed design, Non-goals) whose observable outcome is stated only in that reasoning and appears in neither the staged spec text nor the `docs/` page that owns it — including the case where adversarial review deferred the *mechanism* to a later proposal but left the resulting accepted behavior undocumented in the text that lands now. Deferring a fix does not defer documenting the accepted behavior. The fix is a spec and/or doc edit that states the outcome the reader or operator observes at the section and page that own it, and an "Edge cases and accepted failure modes" row that points to that landing text.
- A new operator-facing failure mode, or a **new cause** of an existing failure or data-loss path, absent from the *narrative* operator docs (`docs/operator-guide/`, `docs/runbooks/`) that enumerate that failure's causes. This is distinct from the companion-row class above (a metric's `metrics.md` row, an alert's runbook page): it is the failure narrative itself, which lists why the failure happens and must gain the new cause so an operator can recognize it. The fix is a docs edit adding the cause to the narrative that owns it.
- A behavior the proposal changes with no test listed in the Testing section, an omitted tier the change plainly reaches (per `test-coverage.md`), or a listed test that exercises only the happy path where the change adds an error, concurrent, boundary, security or fail-closed, or spec-named-failure path (for a security or fail-closed change, no test asserting the deny path; for a recovery, idempotency, or dedup change, no test asserting the replay/crash/failover path). The fix is a Testing-section edit that names the specific, insightful tests to add during implementation. An additional nice-to-have test beyond that required coverage is not a finding.

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

The workflow script lives at `.claude/workflows/change-proposal.js` and is invoked by name. Call `Workflow({name: "change-proposal", args: …})` with the mode-appropriate args:

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

The workflow script is canonical at `.claude/workflows/change-proposal.js`; this file carries the procedure, conventions, and rationale only. Other workflows invoke the script by name (`workflow("change-proposal", args)`), so script edits must keep the args contract stable. When a convergence run surfaces a confirmed error class this file does not list, add it to the error-class list here and, when it fits an existing lens, to that lens's prompt in the script. Keep the finding bar's DO-NOT-report list intact; it is what keeps the loop from converging on nitpicks. When the proposal format conventions and the existing files in `proposals/` disagree, the existing files win; update the conventions list here.
