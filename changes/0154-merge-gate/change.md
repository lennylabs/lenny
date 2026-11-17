# Green main line and merge gate

Status: `Decided`, revision 2, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M1.

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

## Deletion sweep

Decision 5 asks for the files deleted by design-only commits since 2026-10-01 to be listed and checked. The sweep read every non-merge commit on `main` since 2026-10-01 that deletes a file, and kept the commits whose other changes touch only design and records files: `designs/`, `changes/`, `proposals/`, `issues/`, and the root-level markdown records. Six commits qualify. Each deleted file and the reason for its deletion follow.

- `hack/boilerplate.go.txt`, deleted by `8ef08bfa8` (2026-11-07, "designs: add allow-list mode to user credential request rules"). The deletion was accidental. The commit edits a design, and nothing in it concerns code generation, but `make generate` and `TestDeepCopyInSyncWithGoTypes` read the file. Step 0154.S1 restores it from the commit's parent.
- `designs/experiment-provider-removal-design.md`, deleted by `5f18f1598` (2026-11-08). The deletion was intended: the commit replaces that design with the runtime routing design.
- `designs/session-replay-design.md`, deleted by `bba749f3c` (2026-11-04). The deletion was intended: the commit merges the derive and replay designs into one design with two modes.
- `designs/tenant-owned-pools-design.md`, deleted by `edac8705e` (2026-11-04). The deletion was intended: the commit message names the removal of that design in favor of derived runtimes.
- `designs/session-derive-design.md`, `designs/user-git-identity-design.md`, `designs/evidence/README.md`, and `designs/evidence/service-account-credentials-design/README.md`, deleted by `2101789b5` (2026-11-16, step 0113.S2). The deletions were intended: the commit moves every design into its change folder, and the content is under `changes/0144-session-derive/`, `changes/0152-user-git-identity/`, and `changes/0143-service-account-credentials/evidence/`.
- `tests/registers/proposal-label-commits.yaml` and `tests/tier0_static/proposal_label_commit_test.go`, deleted by `791ebcbc9` (2026-10-03). The deletions were intended: the commit reverts a commit-message lint that the 0079 design-conformance review added out of scope, and it changes nothing else.

`hack/boilerplate.go.txt` is the only accidental deletion the sweep found.
