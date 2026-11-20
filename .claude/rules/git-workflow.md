# Git workflow

Project-wide rules for how branches are integrated and retired. They apply to every branch in this repository and to any agent or workflow that merges work or manages branches. They complement the harness git conventions rather than restate them.

## Top-level principle

Integration history is explicit, `main` stays green, and branches are short-lived. A feature branch merges only on a green merge-gate verdict for its head, lands through a merge commit that records the integration, and is deleted once it is merged so stale branches do not accumulate.

## Merge only on a green gate verdict

- A branch merges only when the merge-gate verdict for its head commit is `PASS`. `lenny-test gate` runs the merge gate in the gate container and writes the verdict to `tests/results/gate-<run>.json`; `.claude/rules/test-coverage.md` ("Gates") states the tiers it runs. The verdict counts for the head only when its `trigger.git_revision` is the branch head, its `gate.dirty` is false, and its `gate.integration_merge_base` is the output of `git merge-base refs/heads/main <head>`. A commit added to the branch after the run, including a conflict resolution, needs a new verdict.
- A records-only commit is the one exception: a commit after a `PASS` verdict keeps that verdict when both of the following hold.
  1. Every path the commit touches is under `changes/` (the step's digest, which records the verdict, the step statuses, and the board), or is `docs/about/status.md` with every changed line between its `<!-- generated:status -->` and `<!-- /generated:status -->` markers.
  2. The static-tier records checks and the secret scan pass on the commit: `python3 scripts/changes/changes.py check`, `python3 scripts/changes/changes.py board --check`, `python3 scripts/changes/changes.py status-page --check` when `changes.py` has the `status-page` command (change 0115 adds it), and `lenny-test secretscan`, which runs the static tier's secret scan on the committed tree at `HEAD` and passes only with exit code 0 and the verdict `PASS`. An `UNVERIFIED` result, such as on a host without the gitleaks release the gate image pins, does not pass (change 0158, decision 10).

  A commit that touches any other path, or any line of `docs/about/status.md` outside the markers, needs a new verdict.
- The merge commit message or the step's digest names the verdict's run identifier, so the green run behind each merge can be found.
- `main` stays green. A merge-gate failure on `main` is fixed or reverted before any other branch merges. Revert the merge that introduced the failure with `git revert -m 1 <merge>` when the fix is not immediate.

## Review changes to the merge gate

- The merge gate builds `lenny-test` from the branch under test, so a branch that changes the harness is judged by its own version of it, and a branch could turn a check off and still pass. A branch whose diff against its merge base with `main` touches `cmd/lenny-test/`, `tests/gate/`, `tests/static/`, `tests/registers/`, `tests/flake-budget.yaml`, `.gitleaks.toml`, `.golangci.yml`, a `.gitattributes` file in any directory, `.github/workflows/release.yml`, `scripts/refactor/manifest`, `go.mod`, `go.sum`, `go.work`, `go.work.sum`, or `vendor/` is reviewed with the `review` skill over that diff before it merges. The move manifest is on the list because the shrink-only tests of the gateway layout and wiring registers map renamed rows back through it (change 0117, decision 14). The secret scan's registers of reviewed binary files and skipped paths are under `tests/registers/`. The golangci-lint configuration decides which linters and files the static tier's lint reads, and a `.gitattributes` entry changes how git reads a file, including which files the secret scan reads as binary (owner's answer to GT-2, 2026-11-19). The static tier's lint reads the release build targets from `.github/workflows/release.yml`, so an edit there changes which builds its check of the product packages lists. A workspace file and a vendor directory change the source the go command builds from (owner's answer to GT-19, 2026-11-20). Scripts and the packages `lenny-test` imports are outside this rule (change 0158, decision 6).
- The review checks whether the diff removes, weakens, or skips a check, changes what a check reads, widens the quarantine, adds an allowlist entry for a value that is not a fake secret by design, adds or updates a register entry for a binary file or a skipped path without reviewing that file's content, or adds or changes a module dependency in a way that puts new code into `lenny-test` or the checks it runs. A finding that needs the owner's decision blocks the merge until the owner settles it.
- The step's digest, or the merge commit message when there is no digest, records the review's outcome.
- The owner revisits this rule when pull requests replace direct merges (change 0158, decision 1).

## Merge with a merge commit, not a fast-forward

- `main` is the integration branch. Cut every proposal, draft, fix, and records branch from `main`, and merge it back into `main`. Merges are direct until a first version ships; pull requests replace them after that.
- Integrate a feature branch into a long-lived branch (parent branch unless the user asks for something else) with `git merge --no-ff`, so the integration is a single merge commit that names the branch and groups its commits. Do not fast-forward a feature branch into a long-lived branch, even when the long-lived branch has not advanced.
- The merge commit message names the proposal, finding, or feature the branch implemented, so the integration point is greppable from the first-parent history.
- A merge that conflicts is resolved on the feature branch (rebase or merge the target in, resolve, re-run the merge gate on the new head), then integrated with `--no-ff`. Do not resolve conflicts directly in the merge into a long-lived branch.

## Delete a branch once merged

- Delete a feature branch immediately after it merges, both the local branch (`git branch -d`, which refuses an unmerged branch) and its remote counterpart (`git push origin --delete <branch>`) when it was pushed.
- Use the safe `-d` form rather than `-D`; a branch that `-d` refuses is not fully merged and must not be force-deleted without establishing where its unique commits went.
- A long-lived integration branch (`main`) is never deleted by this rule; it names the branches it protects.

## Where these rules apply

- Every merge, and the review of a branch that changes the merge gate.
- Branch cleanup after a merge.
- The state of `main` between merges.

## How to apply when merging

1. Confirm the feature branch's work is complete, run `lenny-test gate` on its committed head, and confirm the verdict is `PASS` for that commit with a clean tree. Do not merge on any other verdict. When a records-only commit follows the verdict, run the records checks and `lenny-test secretscan` on that commit, and confirm that each passes, before relying on the verdict.
2. Run `git merge-base refs/heads/main <head>` and confirm that its output equals the verdict's `gate.integration_merge_base`. The full ref name keeps a tag named `main` from standing in for the branch, as in the gate. When the two differ, or the verdict carries no `gate.integration_merge_base`, run the gate again on the head rather than merge.
3. When the branch's diff touches a path that "Review changes to the merge gate" names, confirm that the `review` skill ran over that diff and that no finding awaits the owner.
4. Merge into the target with `git merge --no-ff`, with a message naming what the branch implemented.
5. Delete the local branch with `git branch -d`, and delete the remote branch with `git push origin --delete` when it was pushed.
6. Push the updated integration branch.
7. When the merge-gate tiers fail on `main` afterwards, fix or revert before the next merge.

## Escape hatches

- Pulling an unchanged upstream into a tracking branch (`git pull` with no local commits) fast-forwards normally; this rule governs integrating a feature branch, not syncing a branch with its own upstream.
- A branch preserved deliberately for an in-flight or audited line of work is kept until that work concludes, rather than deleted on merge; state why it is kept.

## Maintenance

When a branch-integration or cleanup gap surfaces that this file does not cover, add a specific, actionable rule. Keep the file to branch integration and retirement; commit-message and authorship conventions live with the harness conventions.
