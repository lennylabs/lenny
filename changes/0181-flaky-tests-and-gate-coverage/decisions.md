# Decisions: 0181 Flaky test fixes and gate coverage across tiers

## Open decisions

### O2 Where the embedded-manifest check runs (GATE-I17)

`TestEmbeddedManifestsMatchDevProfileRender_spec_17_4` is a general documentation gate (`gateDocsFiles`), which the gate on `main` runs and the check before a code merge does not. On the host it always fails, because the host's helm renders differently from the helm `tests/gate/Dockerfile` pins.

- **(a) A generator check in the static tier.** Move the check to `tests/static`, where the static tier of the check before a code merge runs it in the gate image on every branch, and skip it with a `blocked:` reason outside the gate image when the host's `helm version` differs from the `HELM_VERSION` that `tests/gate/Dockerfile` pins, so a host run reports a skip rather than a failure. The rule texts stay as they are; `TESTING.md` and `.claude/rules/test-coverage.md` ("Documentation checks") already name a generator check as a kind of check.
- **(b) A rule.** Keep the check in the docs tier, and add to the v1 merge policy in `.claude/rules/git-workflow.md` that a branch that touches `charts/` or `pkg/embedded/manifests/` runs `lenny-test run --tier docs --container` before it merges. No code changes.
- **(c) (b) with the host skip of (a)**, so the host docs tier also stops reporting the check as one of its known failures.

Recommendation: (a). The check then runs in the gate image on every code merge without a step to remember, and the host docs tier drops to the 18 failures of GATE-I9.

### O3 Adopting coverage across tiers (GATE-I14)

Step 0181.S4 builds CV-1 to CV-4 and measures the time they add to the check before a code merge, under "Validation owed" in `steps.md`. Adopting it amends [0172 decision 9](../archive/2026-11-26-0172-gate-speed/decisions.md), under which `--cover` writes the unit run's profile, and the rule texts that say so: "v1 merge policy" in `.claude/rules/git-workflow.md` and the `lenny-test gate merge` item of "How to run" in `.claude/rules/test-coverage.md`.

- **(a) Adopt.** Merge 0181.S4, and edit the two rule texts and `changes/programs/v1.md` where they describe the profile, in the same step.
- **(b) Adopt behind a bound.** Merge 0181.S4 only if the measured addition is below a bound the owner sets, such as 10% of the check's wall time, and otherwise keep the waivers.
- **(c) Do not adopt.** Close GATE-I14 as declined, and keep waiving the floor for code that only the component tier reaches.

Recommendation: given after the measurement, in the step's digest and in this entry.

## Decisions

1. **The Postgres start timeout (GC-1, O1).** `PostgresOptions.withDefaults` raises the default `StartupTimeout` from 30 to 60 seconds, the bound of the MinIO and fake GCS fixtures, and `SharedPostgres` takes the same default through it. The timeout bounds each attempt of the retry `startContainer` runs while the session's reaper is not running (GATE-I18), so a start that waits for a stopped reaper keeps its full 60 seconds. The next runs of the gate-main workflow show whether the timeout recurs. Step 0181.S2 builds it and closes GATE-I16. The options not chosen were (b), keeping 30 seconds and bounding how many Postgres containers one test binary starts at once with a semaphore in `runPostgres`, and (c), both. Owner's answer to GC-1 on the M3 readiness decision page, answered in chat 2026-11-27, with the words "GC-1: (a)".
