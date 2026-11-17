# Git workflow

Project-wide rules for how branches are integrated and retired. They apply to every branch in this repository and to any agent or workflow that merges work or manages branches. They complement the harness git conventions rather than restate them.

## Top-level principle

Integration history is explicit, `main` stays green, and branches are short-lived. A feature branch merges only on a green merge-gate verdict for its head, lands through a merge commit that records the integration, and is deleted once it is merged so stale branches do not accumulate.

## Merge only on a green gate verdict

- A branch merges only when the merge-gate verdict for its head commit is `PASS`. `lenny-test gate` runs the merge gate in the gate container and writes the verdict to `tests/results/gate-<run>.json`; `.claude/rules/test-coverage.md` ("Gates") states the tiers it runs. The verdict counts for the head only when its `trigger.git_revision` is the branch head and its `gate.dirty` is false. A commit added to the branch after the run, including a conflict resolution, needs a new verdict. The one exception is a commit that touches only the change's records under `changes/` (the step's digest, which records the verdict, the step statuses, and the board), provided `python3 scripts/changes/changes.py check` passes on it.
- The merge commit message or the step's digest names the verdict's run identifier, so the green run behind each merge can be found.
- `main` stays green. A merge-gate failure on `main` is fixed or reverted before any other branch merges. Revert the merge that introduced the failure with `git revert -m 1 <merge>` when the fix is not immediate.

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

- Every merge.
- Branch cleanup after a merge.
- The state of `main` between merges.

## How to apply when merging

1. Confirm the feature branch's work is complete, run `lenny-test gate` on its committed head, and confirm the verdict is `PASS` for that commit with a clean tree. Do not merge on any other verdict.
2. Merge into the target with `git merge --no-ff`, with a message naming what the branch implemented.
3. Delete the local branch with `git branch -d`, and delete the remote branch with `git push origin --delete` when it was pushed.
4. Push the updated integration branch.
5. When the merge-gate tiers fail on `main` afterwards, fix or revert before the next merge.

## Escape hatches

- Pulling an unchanged upstream into a tracking branch (`git pull` with no local commits) fast-forwards normally; this rule governs integrating a feature branch, not syncing a branch with its own upstream.
- A branch preserved deliberately for an in-flight or audited line of work is kept until that work concludes, rather than deleted on merge; state why it is kept.

## Maintenance

When a branch-integration or cleanup gap surfaces that this file does not cover, add a specific, actionable rule. Keep the file to branch integration and retirement; commit-message and authorship conventions live with the harness conventions.
