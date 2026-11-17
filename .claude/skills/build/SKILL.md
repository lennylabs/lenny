---
name: build
description: Carry out one step of a change, or several small steps that share a branch, on its own branch - the specification edits first, then the code and the tests, then the documentation - iterating with lenny-test --changed, writing the step's digest, and stopping on any choice the change does not settle. Bound to the layout in changes/README.md. Use when the owner asks to build a step.
argument-hint: <step> [<step> ...]
---

# Build

Build the named steps of a change on one branch, so the owner can review and merge them.

## Before starting

1. Read the change: `change.md`, `decisions.md`, and the step entries in `steps.md`. Read the program's "Reading the program" section if the change belongs to one.
2. Check that every step in `depends-on` without `(part)` is `Landed`, and that no open decision in `decisions.md` touches the step. If either fails, stop and report what blocks it. A `(part)` dependency that has not landed means the step's description names a part that waits; leave that part out and say so.
3. Cut the branch from `main`: `change/<step>-<slug>`, such as `change/0127.S3-pod-git-path`, or with the first step's identifier when several steps share it.
4. Set each step to `Building` with `python3 scripts/changes/changes.py step <step> Building`, set the change to `In progress` if it is not, regenerate the board and the status page (`board` and `status-page`), and commit.

## Building

Work through the step's lanes in order:

1. **Specification.** Edit `spec/` as the change states: the requirements it adds, modifies, or retires, and the definitions and values it sets. Follow `.claude/rules/spec-citations.md` and `.claude/rules/doc-style.md`. Commit.
2. **Code and tests.** Implement against the specification as it now stands. Follow `.claude/rules/code-best-practices.md` and `.claude/rules/test-coverage.md`: tests for every tier the step reaches, with their annotations, covering the error, boundary, and concurrent paths. Iterate with `lenny-test --changed --max-tier <tier>` until it passes, and check coverage with `lenny-test coverage --diff main`. Commit in coherent pieces.
3. **Documentation.** Edit each documentation page the change's "Documentation" section names for this step, and any page that names a setting, metric, error, or command the step adds, renames, or removes. Follow `.claude/rules/doc-content.md` and `.claude/rules/doc-style.md`. Commit.

Run the static tier and every tier the step reaches before finishing, then run the merge gate on the committed head: `lenny-test gate` (`.claude/rules/test-coverage.md`, "Gates"). Report the step only on a `PASS` verdict for the head commit with a clean tree. The digest commit that follows keeps that verdict only when it is a records-only commit as `.claude/rules/git-workflow.md` defines it: it touches nothing outside `changes/` and the generated plan section of `docs/about/status.md` (between the `<!-- generated:status -->` markers), and `python3 scripts/changes/changes.py check`, `board --check`, and, where `changes.py` has the command, `status-page --check` pass on it. Any other commit after the verdict needs a new gate run. A merge-gate failure is fixed on the branch, or, when it also fails on `main`, reported as a failure on `main` to fix or revert before the branch merges; it is never recorded as pre-existing. A failure that also occurs on `main` in a tier outside the merge gate is recorded in the digest as pre-existing rather than fixed here.

## Stopping on a choice

When a choice comes up that neither the specification nor `decisions.md` settles, do not pick one silently. Add it to "Open decisions" in `decisions.md` with its options and a recommendation, commit, and continue with the work the choice does not touch. Report every such decision at the end.

## Finishing

1. Write the digest with the `digest` skill: `CGO_ENABLED=0 go run ./scripts/specshift digest <step>` (or `<first>-S<last>`, such as `0113.S1-S3`, for shared steps) writes `digests/<step>.md` in the change folder from the branch and its merge base with `main`. Act on what it lists, then fill in its gate results, review outcomes, and defaults applied, as the `digest` skill states. The gate results name the merge-gate verdict's run identifier, its commit, and its result, the pre-existing failures outside the merge gate, and any coverage waiver for wiring code with the code it names. For a step with no specification change, when the generator cannot run, write the digest by hand under the same headings: the steps and their commits, the requirements added, modified, or retired, the documentation pages edited and their readers, the tests and tiers run with their results, the merge-gate verdict, the pre-existing failures, and every default applied without asking.
2. Run `python3 scripts/changes/changes.py check`, `board`, and `status-page`, then `board --check` and `status-page --check`, and commit only those records.
3. Report to the owner: the branch, the digest, the merge-gate verdict and its run identifier, the test results, and the open decisions raised. Leave the steps `Building`. When the owner merges the branch, set each step to `Landed`, set the change to `Implemented` when its last step lands, and regenerate the board and the status page.
