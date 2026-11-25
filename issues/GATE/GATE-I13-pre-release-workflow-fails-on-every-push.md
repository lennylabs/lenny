---
id: GATE-I13
status: open
kind: build-gap
severity: medium
requirements: []
---

# The pre-release workflow fails on every push to `main` with a workflow file error

## Specification

None: the defect is in the CI workflows, which `.github/workflows/README.md` describes. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate and the test harness.

## Evidence

- `gh run list --workflow pre-release.yml` lists a failed run, with a duration of 0 s and the trigger `push`, for each of the latest 30 pushes to `main`, the earliest on 2026-07-16. `gh run view 45695022042` reports "This run likely failed because of a workflow file issue."
- `.github/workflows/pre-release.yml` declares only `workflow_dispatch` under `on`, and its YAML parses, so the error lies in what GitHub validates beyond the YAML, such as an expression, a job reference, or a reusable workflow path.
- `actionlint` 1.7.12, run over `.github/workflows/` on 2026-11-25 by step 0172.S11, names two errors in `pre-release.yml`: its `uses: ./.github/workflows/weekly.yml` calls a workflow whose `on:` has no `workflow_call` trigger, and its step condition `if: ${{ secrets.PENTEST_BUNDLE_JSON != '' }}` reads the `secrets` context, which a step's `if` cannot read. The same reusable-workflow error chains through `weekly.yml`, which calls `nightly.yml`, and `nightly.yml`, which calls `pr.yml`, none of which declares `workflow_call`. The fix adds `workflow_call` to the three called workflows, or replaces the calls, and moves the secret into an environment variable that the condition reads; it changes what the paused workflows run, so 0172.S11 leaves it to this issue.

## Gap

Every push to `main` records a failed workflow run, so a real failure of a workflow that runs on `main`, such as the gate workflow of change 0172 decision 27, is harder to see among them.

## Suggested resolution

Run `actionlint` over `.github/workflows/`, fix the error in `pre-release.yml`, and confirm that a push to `main` records no run of it.
