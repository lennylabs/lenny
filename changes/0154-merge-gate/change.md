# Green main line and merge gate

Status: `Draft`, revision 1, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M1.

## Problem

Tier 0 fails on `main`, and nothing states which tiers must pass before a branch merges. Of its four failures, two are tests that cannot run on macOS and one is a defect in the tree: commit `8ef08bfa8` deleted `hack/boilerplate.go.txt` on 2026-11-07, which `make generate` needs, and the red tier hid it for nine days. No run of the `pr` workflow has passed since at least 2026-10-15. A tier that is always red stops catching anything ([test infrastructure review](evidence/test-infrastructure-review.md), "Question 1" and "Merge policy").

## Scope

- **In scope.** Restore `hack/boilerplate.go.txt`. Make the runtime supervisor binary tests skip off Linux with a registered reason. A container image that pins the merge gate's toolchain (Go, Helm, `buf`, `controller-gen`, and the C toolchain), and a `lenny-test gate` command that runs the merge-gate tiers in it and writes a verdict. Static checks run concurrently and report every failure. The merge policy in `.claude/rules/test-coverage.md`, `.claude/rules/git-workflow.md`, and the `build` skill: a branch merges only on a green verdict for its head, `main` stays green, and flaky tests go to a capped quarantine. `.github/workflows/pr.yml` repaired to run the same gate, or disabled (decision O2). The tier-0 category D gates, which pin comment wording, deleted after confirming the comments they pin are correct. A sweep of files deleted by design-only commits since 2026-10-01, to find other accidental deletions.
- **Out of scope.** Splitting and renaming the tiers (0155), deriving coverage and selection (0116), and new kinds of tests (0156, 0157).

## High-level design

One canonical environment decides the gate: a Linux container built from the repository. `lenny-test gate` runs tier 0, tier 1 on the changed packages and their dependents, tiers 2 to 4 as `--changed` selects, and the general documentation gates, and writes a verdict file whose run identifier the step's digest records. A failure on `main` is fixed or reverted before other merges proceed. The gates that only run before an area milestone or a release are listed in "Tier boundaries and gates" of the review and become the area and release gates of 0155.

## Current state

- Tier 0 is about 322 tests and takes about 56 seconds of Go test time on the owner's machine, one test alone taking 35 seconds.
- `TestDeepCopyInSyncWithGoTypes` fails because `hack/boilerplate.go.txt` is missing; the two runtime supervisor binary checks need a Linux toolchain.
- `.github/workflows/pr.yml` was cancelled 27 times and failed 4 times with no success in the 200 runs between 2026-10-15 and 2026-11-12.

## Specification and non-specification changes

No specification text changes; this change implements the testing model under the escape hatch for test harnesses in `spec-driven-development.md`. Non-specification: `hack/boilerplate.go.txt`, `cmd/lenny-test` (the gate command and concurrent static checks), a container definition under `tests/`, `.github/workflows/pr.yml`, the rules named under "Scope", the `build` skill, and the deleted category D tests.

## Documentation

Contributors: `TESTING.md` and `docs/about/contributing.md` describe the merge gate and how to run it on macOS through the container.

## Tests

The gate command and the verdict file are tested in `cmd/lenny-test`; the merge gate passes on `main` after this change, in the container.
