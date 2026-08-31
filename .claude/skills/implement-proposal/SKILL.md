---
name: implement-proposal
description: Implement an approved proposal end to end by running its implementation checklist as one sequence, landing each spec-lane step's staged edits under a scoped write lease and building each code-lane step with tests, then closing the BUILD-GAPS.md findings that reference it. Pass "spec-only" to run the leading spec-lane prefix and stop. Use after a proposal is approved.
argument-hint: <path to a proposal> [spec-only]
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Implement proposal

This skill takes an approved proposal and carries it through to implementation. It is the implementation stage of the pipeline: `change-proposal` writes and converges a proposal, a human approves it, and this runs it.

## One execution sequence

There is no spec phase and no build phase. There is **one sequence, the proposal's implementation checklist**, and each step is dispatched on its lane:

- a **`spec`** step applies the `SPEC-n` deliverables its line names, under a write lease scoped to exactly the files those deliverables target, verifies what landed against what the proposal stages, commits, and ticks its box;
- a **`code`**, **`schema`**, **`migration`**, **`test`**, or **`docs`** step builds, through the loop below, and ticks the same box.

Progress is therefore per-deliverable, in the checklist, which is also the resumption record. That is why `.status.md` carries four states and no fifth: "the spec is applied" can only be a status if spec application is a phase, and it is not one.

**One lane per step**, as a hard rule: the lane selects the handler, and a step naming both a spec deliverable and a non-spec one has none. An absent or unrecognised lane stops the run rather than being guessed.

**Spec steps lead, as the standard pattern.** An interleave is permitted, must state on its own line why it is necessary, and qualifies only when the spec text cannot be written or applied until the earlier step lands — the staged edit is the output of a tool this proposal builds, or its content depends on a fact only the built artifact fixes. Efficiency and convenience do not qualify. The `applicability` lens enforces this when the proposal is reviewed.

The safety the old spec-first phase provided is not lost. It protected code from being written against unlanded spec text by landing all of it first, which said nothing about whether the *right* statement had landed. The rule is now that **a step may only depend on spec deliverables whose steps are already ticked**, which is checkable from the checklist's own `Depends on` lines and is the sharper statement.

## The spec/ write lease

`spec/` is read-only unless a lease is open. The lease names one proposal, one step, and the exact files that step's deliverables target, and it expires.

- It is opened for a `spec`-lane step and released when that step ends, **pass or fail**.
- A `code`-lane step holds none, and **refuses to start if one is somehow still held**, which is the check that makes the invariant hold rather than merely be intended. That closes the one hazard interleaving introduces: a spec step that died mid-flight leaving `spec/` writable for whatever runs next.
- Every failure path refuses: no lease, a malformed lease, an expired one, an unreadable status, or a path outside the allow list. There is no path that allows on an error.

One honest limit: the hook trusts the lease, and the lease file is not under `spec/`, so nothing stops an agent writing one. That raises the bar from "any approved proposal anywhere unlocks everything" to "an agent must forge a lease naming an Approved proposal", which is the right bar for a threat model of accidental writes. It is not a sandbox.

Check it with `node .claude/tools/spec-lease.mjs status`. Release a stale one with `release`; that is an operator action, never automatic.

## What the proposal gives you

- **`.summary.md`** is injected into every agent in the run: the top-level changes, the decisions that are closed, and the traps. It is the one file all of them read.
- **`.implementation-checklist.md`** is the sequence, maintained while the proposal was reviewed. The Plan phase carries its steps across with their ids, lanes, deliverables, tiers, and dependencies rather than inventing an order. Pass `plan` to skip planning entirely when the checklist already carries everything.
- **`.deviations.md`** is written by this skill and read by every reviewer in it. An `accepted` entry is adjudicated and must not be re-raised.

An imperfect checklist does not stop the run. A step whose surface is already present is marked `alreadyDone`, one whose prerequisite is elsewhere is re-sequenced, one that cannot be built as written is built from the proposal's stated intent, and only when the intent cannot be recovered does it carry a `blocked` reason. Every mismatch is reported in `checklistDeviations`.

## The step build loop

For a non-spec step:

```
0.  compile gate         go build over the changed packages, and assert no lease is held
1.  implement, or fix what the last round left
2.  conformance and invariants review of the step's diff       <- no tests run
3.  findings? fix and return to 2
4.  tests, scoped to what the fix warrants
5.  failures? fix and return to 2, which re-checks conformance before re-testing
6.  FINAL GATE: the full tier set the checklist names, over the finished tree
      not green -> record the miss, then back to the fix loop; the gate re-runs
7.  tick the checklist box
```

The review runs **before** the tests because most fixes in a step answer that review, and running the tier set after each of them ran the same suites over and over while they stayed green. The compile gate exists because a reviewer handed code that does not build produces confident nonsense.

**A step is never ticked on a gate that did not pass.** The full set runs against the exact tree being ticked, which is the guarantee every scoped run in between borrows against.

### Which tiers a fix runs

Two mechanisms, because "be smarter" has to be made mechanical or it regresses to running everything.

**The classification is grounded in a real diff.** The scoping agent is handed a git range and reads the diff itself. The fixer's account of what it changed is adversarial input: where the two disagree the diff wins, and the disagreement is reported on its own, because a fixer that changed more than it said is worth knowing about.

**The three cheapest classes are decided by a script.** `classify-diff.mjs` decides `comment-only`, `doc-only`, and `test-only` mechanically, and its verdict is authoritative where it fires. That closes the failure this exists for: a fixer adjusts one line of logic while editing the comment above it, reports "updated a comment", and every test that would catch it is skipped. A class maps to a minimum tier set; going above it needs a named hunk, and going below it is allowed only where the script has already decided.

**A cost ledger** at `scratchpad/test-times/<branch>.json` records what each tier has actually cost. A tier whose median exceeds `expensiveTierSeconds` runs only when a named hunk requires it. The verifier that ran the tiers appends its own timings, since it knows them.

**Every final-gate failure is a scoping miss** and is recorded with the classification and the skip that caused it, then returned as `gateMisses`. That is the only direct evidence available about whether the class table is calibrated; a class that keeps appearing there gets its minimum tiers raised.

### When a step will not converge

Three conditions stop a step, and nothing else does: the attempt cap, consecutive dead agents, and an `unproductive` verdict from the judges. Everything else is a **detector** that wakes the judges — the attempt cadence, `maxPhaseOscillations` on the conformance/test cycle, and `maxFinalGateFailures`. A counter cannot tell a hard-but-converging step from a stuck one, so its output is a reason to look. A fired detector re-arms rather than re-firing, so the machinery meant to diagnose an expensive loop does not become the expensive part of it.

The judges return one of three verdicts. `resolvable` is the default. `unresolvable` means no legal change closes the finding, because the landed code is right and the proposal is wrong and the proposal is read-only here; the judges write an **accepted deviation**, the finding is set aside, and the step continues. `unproductive` means a legal change exists and this loop is not making it; the step stops and the outstanding work goes to a human, and no deviation is written, because setting it aside would tick a step over a real gap.

`unproductive` needs at least `minUnproductiveRounds` consecutive rounds that committed something and still came back with findings. It is a claim about a pattern, and a fix agent groping toward a hard change looks identical from any one round to one that will never get there.

## Deviations

`.deviations.md` records where the landed code departs from what the proposal states: what it says, what the code does with file:line, why no legal change closes the gap, what a later reader would get wrong, and a suggested next step.

Only the judges accept one. Every reviewer in the run is given the file and told that an `accepted` entry is adjudicated and must not be re-raised in any phrasing; the script also drops a finding whose normalised title matches one, because prompt suppression has been observed to leak. The `acceptedDivergences` argument feeds the same filter, so the two mechanisms are one.

Nothing in this run edits the proposal to close a deviation. That would make the conformance review vacuous and silently amend an approved design. The only edit any agent makes to the proposal is ticking a checklist box, and a dedicated agent does that.

## Hard constraints

- Spec edits land only through a spec-lane step, under a lease, never by hand. An edit whose anchor cannot be located with certainty stops the step rather than being skipped: a partially applied file is a state verification cannot converge out of, because a later discrepancy cannot be told from an edit that never ran.
- Spec content rules hold over verbatim application. The spec never references source paths, cross-references by section number rather than line, and a new section is appended at the end of its level rather than inserted between siblings. The applier records every rule-forced deviation.
- Code follows `.claude/rules/spec-driven-development.md`, `code-best-practices.md`, and `test-coverage.md`.
- No proposal-internal identifiers reach the shipped artifacts. Code, comments, test names, and commit messages cite the spec section or describe the behaviour; they never carry the proposal's own change, decision, review-pass, or step labels, even when `git log` shows prior commits that did.
- Findings close only after the implementation verifies green and the review is clean. The skill never opens or re-opens a finding.
- The workflow sandbox has no filesystem access; see the change-proposal skill. Run `node .claude/tests/run.mjs` after editing a workflow.

## Procedure

### Step 1: preconditions

1. Resolve the proposal path and the optional `spec-only` modifier.
2. Read the status: `node .claude/tools/proposal-status.mjs <proposal> --field status`. It must be `Approved`. Anything else stops the run.
3. Check for a stale lease: `node .claude/tools/spec-lease.mjs status`. One naming another proposal, or an expired one, stops the run and is reported; releasing it is an explicit operator action.
4. Run `git status --porcelain -- spec/` and confirm it is clean, so a step's verification diffs against a clean baseline.
5. Compute `repoRoot` and `date`.

### Step 2: run the workflow

Invoke by `{scriptPath}`, never by name: a name resolves to a cached copy, so a run launched after an edit executes the previous version. The parent invokes the subworkflow the same way, for the same reason.

```json
{
  "proposalPath": "proposals/0081_fix_slug",
  "repoRoot": "/abs/path",
  "date": "2026-08-31",
  "implementCode": true,
  "specReviewFocus": [],
  "acceptedDivergences": []
}
```

Arguments, all with defaults: `implementCode` (true; false runs the leading spec-lane prefix and stops), `maxPlanRounds` (2), `maxStepAttempts` (50), `maxDeadAttempts` (3), `maxReplans` (6), `replanEvery` (4), `replanStruggleAttempts` (4), `maxVerifyRounds` (25), `maxReviewRounds` (50), `coverageFloor` (80), `introspectEvery` (5), `minUnproductiveRounds` (5), `maxPhaseOscillations` (5), `maxFinalGateFailures` (5), `expensiveTierSeconds` (300), `leaseTtlHours` (24), `reverifyDoneSteps` (false), `skipBuild` (false), `plan`, `specReviewFocus`, `acceptedDivergences`. Raise a bound only with a reason; a loop that needs a larger one usually has a cause the bound will not fix.

`spec-only` runs the leading spec-lane prefix. Under the standard pattern that prefix is every spec step and the mode is total, which is what `close-build-gaps.sh --mode proposals` relies on. On a proposal that genuinely interleaves it is not, and the run returns `spec-only-incomplete` naming the step behind the interleave rather than silently skipping it.

Run with a strong model at high effort. Before a run whose steps reach tier 5 or above, work through the Environment preflight in `.claude/rules/test-coverage.md` yourself: stale images, terminal pods from an earlier run, a warm pool that cannot reach Ready, and orphaned envtest processes all produce failures that look exactly like a defect in the step under test.

### Step 3: interruptions

Stop the stale task with `TaskStop`, then relaunch with `{scriptPath, resumeFromRunId}`. Steps commit as they land and tick their boxes, so a resumed run skips what is done. Relaunching fresh is often better once several steps have landed: their boxes are ticked, and passing `plan` with only the remaining steps avoids re-deriving a sequence the checklist already states. Check the lease first: an interrupted spec step may have left one open.

### Step 4: report

1. Run `git status --porcelain` and `git log --oneline` for the run's commits. Confirm spec commits are per spec step, code is under `pkg/`, `cmd/`, `charts/`, `schemas/`, and `tests/`, and `BUILD-GAPS.md` carries the closed findings.
2. Report `deviationsFile` and its contents whenever entries exist. This is the run's statement of what did not land as proposed, and it is the input a human needs to decide whether the proposal or the code was wrong.
3. Report `gateMisses` whenever non-empty: each is a tier the scoped runs said could not be affected and the full pass proved otherwise.
4. Report `proposalEdits` whenever `edited` is true. An agent modified the proposal despite the instruction; the edit was kept rather than reverted, so a human decides.
5. Report `reverifyRepaired`, `checklistDeviations`, and `skippedSteps` whenever non-empty. A run that recovered from a mis-ordered checklist and said nothing has hidden a defect in the proposal.
6. On `implemented`: the spec commits, the steps with their commits, the coverage, that the review is clean, and the findings closed. Suggest pushing; do not push unless asked.
7. On `spec-step-failed`: the step, why, and that earlier steps are committed. On `lease-leaked`: a spec step did not release its lease and `spec/` is writable; release it and re-run. On `bad-lane`: the checklist has a step whose lane selects no handler. On `build-step-stuck`: the step, the reason, and the `resumeNote`; when a `stuckFindings` entry is `unproductive`, report its `outstandingWork` as work still to do rather than as a failure of the run.
8. Do not push or open a PR unless asked.

## Relationship to the build loop

`close-build-gaps.sh --mode proposals` drains many approved-proposal findings autonomously; it lands each proposal's spec through this skill in spec-only mode and implements the code itself. Use this skill directly for one proposal with a large or ordering-sensitive blast radius, and the build loop to drain many.

## Maintenance

The workflows are canonical at `.claude/workflows/implement-proposal.js` and `implement-proposal-build.js`; this file carries the procedure and the rationale. The behavioural tests are `.claude/tests/step-loop.test.mjs` and `implement-proposal-*.test.mjs`, and the guard's own test is `.claude/tests/hook.test.sh`; run `node .claude/tests/run.mjs`. When implementation surfaces a recurring planning or sequencing gap, strengthen the completeness critic in the subworkflow.
