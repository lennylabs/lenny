# Git workflow

Project-wide rules for how branches are integrated and retired. They apply to every branch in this repository and to any agent or workflow that merges work or manages branches. They complement the harness git conventions rather than restate them.

## Top-level principle

Integration history is explicit, `main` stays green, and branches are short-lived. A feature branch merges only on a green merge-gate verdict for its head, lands through a merge commit that records the integration, and is deleted once it is merged so stale branches do not accumulate.

## Merge only on a green gate verdict

<!-- SUSPENDED for the v1 program (owner's decision of 2026-11-23, while the owner is the only contributor). Restore the text in this comment, and delete the v1 text that follows it, when the v1 program completes or when a collaborator joins, whichever comes first. The v1 program record (changes/programs/v1.md, "Decisions") lists every suspended block.

- A branch merges only when the merge-gate verdict for its head commit is `PASS`. `lenny-test gate` runs the merge gate in the gate container and writes the verdict to `tests/results/gate-<run>.json`; `.claude/rules/test-coverage.md` ("Gates") states the tiers it runs. The verdict counts for the head only when its `trigger.git_revision` is the branch head, its `gate.dirty` is false, and its `gate.integration_merge_base` is the output of `git merge-base refs/heads/main <head>`. A commit added to the branch after the run, including a conflict resolution, needs a new verdict.
- A records-only commit is the one exception: a commit after a `PASS` verdict keeps that verdict when both of the following hold.
  1. Every path the commit touches is under `changes/` (the step's digest, which records the verdict, the step statuses, and the board), or is `docs/about/status.md` with every changed line between its `<!-- generated:status -->` and `<!-- /generated:status -->` markers.
  2. The static-tier records checks and the secret scan pass on the commit: `python3 scripts/changes/changes.py check`, `python3 scripts/changes/changes.py board --check`, `python3 scripts/changes/changes.py status-page --check` when `changes.py` has the `status-page` command (change 0115 adds it), and `lenny-test secretscan`, which runs the static tier's secret scan on the committed tree at `HEAD` and passes only with exit code 0 and the verdict `PASS`. An `UNVERIFIED` result, such as on a host without the gitleaks release the gate image pins, does not pass (change 0158, decision 10).

  A commit that touches any other path, or any line of `docs/about/status.md` outside the markers, needs a new verdict.
- The merge commit message or the step's digest names the verdict's run identifier, so the green run behind each merge can be found.
- `main` stays green. A merge-gate failure on `main` is fixed or reverted before any other branch merges. Revert the merge that introduced the failure with `git revert -m 1 <merge>` when the fix is not immediate.
-->

**v1 merge policy.** While the owner is the only contributor to the v1 program, a branch merges without a verdict of every merge-gate tier for its head: a code branch merges on a gate run restricted to some tiers, and the gate runs every tier on `main` after the merge.

- **A branch that touches only records and documentation** merges when the records checks and the secret scan pass on its head: `python3 scripts/changes/changes.py check`, `python3 scripts/changes/changes.py board --check`, `python3 scripts/changes/changes.py status-page --check`, and `lenny-test secretscan` with exit code 0 and the verdict `PASS`. A branch that touches `docs/` also runs `lenny-test --tier docs`. Records and documentation are the paths under `changes/`, `issues/`, `docs/`, `.claude/rules/`, and `.claude/skills/`, and the root-level markdown records.
- **Any other branch** merges when these checks pass on its committed head with `main` merged in: `lenny-test gate --tier static,unit,component,contract --cover`, which runs the static tier and the unit, component, and contract tests that the branch's diff against `main` reaches, in the gate container, and writes the unit run's coverage profile; `lenny-test coverage --diff main --threshold 0.80`, which reads that profile; the secret scan; and a targeted run of the tests the branch adds or changes in a tier the gate does not run, such as the security or load_local tier, which the step's digest or the merge commit message names. The gate's verdict records the restriction as `gate.restricted_to` and the profile as `gate.cover_profile`. A branch that touches `docs/` also runs `lenny-test --tier docs`. The integration tier and the general documentation gates run in the gate on `main` after the merge. `lenny-test run --changed` is not the check: it reads the working-tree diff, so on a clean committed head it selects the static tier alone (change 0172, decision 8).
- **A branch that touches `spec/`** also runs the static gates that read the specification, `CGO_ENABLED=0 go test -count=1 ./tests/static/structure/... ./scripts/specshift/...`, because the gate's static tier runs the structure gates and does not run the specification tooling's own tests under `scripts/specshift/`.
- **The gate on `main`.** Run `lenny-test gate` on the head of `main` after a batch of merges rather than after each merge: at least once on each day on which a merge landed, and before a milestone is reported complete. A run is a selected run, which runs the unit, component, contract, and integration tests that the merges since the base reach, or a full run, `lenny-test gate --all`, which runs every package of the unit tier but `tests/docs` and every test of the component, contract, and integration tiers whatever the diff. The run before a milestone is reported complete is a full run, and in each week in which a merge landed, at least one run on a day on which a merge landed is a full run. Every other run is a selected run. A full run uses the same base as a selected run, its `PASS` counts as the latest `PASS` run on `main`, and its verdict records `gate.all` (change 0172, decision 2). Run one run at a time, at a moment when no other tests load the machine. Run it in a clone whose local `main` points at the base: the commit of the latest `PASS` run on `main` whose verdict records no `gate.restricted_to`, or, before the first such run, the last commit of `main` that merged on a pre-merge `PASS` verdict of every merge-gate tier. A run restricted with `--tier`, such as the check before a code branch merges, never sets the base, because it leaves the integration tier and the general documentation gates out. The gate on `main` runs without `--tier` and `--cover`. A run on `main` against itself diffs nothing and selects only the static and docs tiers, so it covers no merge. Against the base, the gate selects every tier the merges since the base reach, and its verdict names the base as `gate.integration_merge_base`. A merge may land while a run is in progress, and merges may land between runs while the latest finished run is `PASS`.
- **A red gate on `main`.** When the latest finished run on `main` is `FAIL` or `UNVERIFIED`, the failure is fixed or reverted before the next merge. When several merges landed since the base, find the merge that introduced the failure by running the failing test on the merge commits in order, and revert that merge with `git revert -m 1 <merge>` when the fix is not immediate. A failure that a rerun on the same commit does not reproduce, such as a container start timeout under load, is a load failure and is rerun rather than fixed.
- **Records.** Each merge commit message names the checks run on the branch head, and the identifier and commit of the latest finished gate run on `main`. A step's digest records the targeted runs, and the gate run on `main` that first covered the step when one has finished.
- The review rule under "Review changes to the merge gate" still applies before a merge.

## Review changes to the merge gate

- The merge gate builds `lenny-test` from the branch under test, so a branch that changes the harness is judged by its own version of it, and a branch could turn a check off and still pass. A branch whose diff against its merge base with `main` touches `cmd/lenny-test/`, `tests/gate/`, `tests/static/`, `tests/registers/`, `tests/flake-budget.yaml`, `tests/non-go-inputs.yaml`, `tests/groups.yaml`, `tests/groups.subsets.yaml`, `.gitleaks.toml`, `.golangci.yml`, a `.gitattributes` file in any directory, `.github/workflows/release.yml`, `scripts/refactor/manifest`, `go.mod`, `go.sum`, `go.work`, `go.work.sum`, or `vendor/` is reviewed with the `review` skill over that diff before it merges. The selection files are on the list because they decide which tests the gate selects for a diff: a row of `tests/non-go-inputs.yaml` that moves a package from `runs` to `tests` takes the package's dependents out of every later selection, and `lenny-test validate` checks only the row's form (change 0172, decision 12). The move manifest is on the list because the shrink-only tests of the gateway layout and wiring registers map renamed rows back through it (change 0117, decision 14). The secret scan's registers of reviewed binary files and skipped paths are under `tests/registers/`. The golangci-lint configuration decides which linters and files the static tier's lint reads, and a `.gitattributes` entry changes how git reads a file, including which files the secret scan reads as binary (owner's answer to GT-2, 2026-11-19). The static tier's lint reads the release build targets from `.github/workflows/release.yml`, so an edit there changes which builds its check of the product packages lists. A workspace file and a vendor directory change the source the go command builds from (owner's answer to GT-19, 2026-11-20). Scripts and the packages `lenny-test` imports are outside this rule (change 0158, decision 6).
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

<!-- SUSPENDED for the v1 program (owner's decision of 2026-11-23, while the owner is the only contributor). Restore the text in this comment, and delete the v1 text that follows it, when the v1 program completes or when a collaborator joins, whichever comes first. The v1 program record (changes/programs/v1.md, "Decisions") lists every suspended block.

1. Confirm the feature branch's work is complete, run `lenny-test gate` on its committed head, and confirm the verdict is `PASS` for that commit with a clean tree. Do not merge on any other verdict. When a records-only commit follows the verdict, run the records checks and `lenny-test secretscan` on that commit, and confirm that each passes, before relying on the verdict.
2. Run `git merge-base refs/heads/main <head>` and confirm that its output equals the verdict's `gate.integration_merge_base`. The full ref name keeps a tag named `main` from standing in for the branch, as in the gate. When the two differ, or the verdict carries no `gate.integration_merge_base`, run the gate again on the head rather than merge.
3. When the branch's diff touches a path that "Review changes to the merge gate" names, confirm that the `review` skill ran over that diff and that no finding awaits the owner.
4. Merge into the target with `git merge --no-ff`, with a message naming what the branch implemented.
5. Delete the local branch with `git branch -d`, and delete the remote branch with `git push origin --delete` when it was pushed.
6. Push the updated integration branch.
7. When the merge-gate tiers fail on `main` afterwards, fix or revert before the next merge.
-->

v1 merge steps, under the v1 merge policy in "Merge only on a green gate verdict":

1. Confirm that the latest finished gate run on `main` is `PASS`, or that its failure has been fixed or reverted.
2. Run the checks the v1 merge policy names for the branch on its committed head, and confirm that each passes.
3. When the branch's diff touches a path that "Review changes to the merge gate" names, confirm that the `review` skill ran over that diff and that no finding awaits the owner.
4. Merge into the target with `git merge --no-ff`, with a message naming what the branch implemented, the checks run, and the latest gate run on `main`.
5. Delete the local branch with `git branch -d`, and delete the remote branch with `git push origin --delete` when it was pushed.
6. Push the updated integration branch.
7. Run `lenny-test gate` on the head of `main` against the base that "The gate on `main`" names, after a batch of merges and at the times that rule states, with `--all` for the full runs it names, when the machine is otherwise idle, and fix or revert a failure before the next merge.

## Escape hatches

- Pulling an unchanged upstream into a tracking branch (`git pull` with no local commits) fast-forwards normally; this rule governs integrating a feature branch, not syncing a branch with its own upstream.
- A branch preserved deliberately for an in-flight or audited line of work is kept until that work concludes, rather than deleted on merge; state why it is kept.

## Maintenance

When a branch-integration or cleanup gap surfaces that this file does not cover, add a specific, actionable rule. Keep the file to branch integration and retirement; commit-message and authorship conventions live with the harness conventions.
