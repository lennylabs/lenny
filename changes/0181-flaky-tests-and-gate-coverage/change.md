# Flaky test fixes and gate coverage across tiers

Status: `Draft`, revision 1, 2026-11-27.

## Problem

Three tests fail without a defect in the code under test, and each failure costs a rerun of a gate run:

- [TOKSVC-I1](../../issues/TOKSVC/TOKSVC-I1-secret-probe-test-reads-before-rbac-grant-takes-effect.md): `TestTokenServiceSecretReadAgainstAPIServer` in `tests/component/secretprobe` probes a Secret as the Token Service user right after it creates the user's `RoleBinding`. The API server's RBAC authorizer reads bindings through informers, so the first probes can run before the grant takes effect. Gate-main workflow run `45869562906` failed the first two cases.
- [GATE-I16](../../issues/GATE/GATE-I16-postgres-container-start-timeout-on-the-ci-runner.md): `containers.StartPostgres` and `containers.SharedPostgres` wait at most 30 seconds for Postgres to accept connections, where the MinIO and fake GCS containers wait 60 seconds. On the 4-vCPU gate-main runner and on a loaded workstation, a burst of parallel container starts exceeded 30 seconds (gate-main run `45872051820`; local gate run `19306af756cd6720d19bf359`), and a rerun on the same commit passed.
- [LOCALDEV-I2](../../issues/LOCALDEV/LOCALDEV-I2-embedded-stack-tests-reserve-ports-then-release-them.md): the tests of `pkg/embedded/stack` reserve a loopback port by opening a listener on port 0, close it, and pass the port to the host-side forwarder, which binds it later. Another test process can take the port in between (gate runs `e1eb9883c44df9d18fea46c6` and `a63d1fdbcf3a1a9c0492aea6`, "bind: address already in use").

Two gaps in the merge gate let a defect through or report one that is not there:

- [GATE-I17](../../issues/GATE/GATE-I17-host-docs-tier-hides-embedded-manifest-drift.md): the check that the embedded manifests match a fresh render of the chart, `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4` in `tests/docs`, fails on every host run of the docs tier because the host's helm renders differently from the helm the gate image pins. Before a merge, a branch that changes `charts/` cannot tell a real drift from the host difference, and the check before a code merge (`lenny-test gate --tier static,unit,component,contract --cover`) does not run the docs tier. Step 0177.S2 merged a drift that then failed the gate on `main` (gate-main run `45913906788`).
- [GATE-I14](../../issues/GATE/GATE-I14-coverage-diff-measures-only-the-unit-tier.md): `lenny-test gate --cover` writes the coverage profile of the unit tier alone, so `lenny-test coverage --diff` counts code that only the component or contract tests reach as uncovered. Two coverage waivers followed from it: EW-W for 0176.S1 ([0176 decision 5](../0176-erasure-waits-for-terminal-pipeline/decisions.md)), and IP-W for 0178.S1 and 0178.S2 ([0178 decision 4](../0178-idempotency-keys-by-principal/decisions.md)).

## Scope

The change covers the three tests and their fixtures, where the embedded-manifest check runs, and which tiers write the coverage profile of a gate run with `--cover`. It changes no requirement of the specification. It leaves out:

- The other 18 failures of the host docs tier, which [GATE-I9](../../issues/GATE/GATE-I9-tests-docs-gap-record-tests-fail.md) records.
- The coverage of the integration tier, which the check before a code merge does not run.
- The coverage of a test that `lenny-test gate --rerun-failed` reruns in the component or contract tier (CV-4).

## Terms

| Term | Meaning |
| :-- | :-- |
| Check before a code merge | The v1 merge policy's check for a branch that touches a path outside the records and the documentation: `lenny-test gate --tier static,unit,component,contract --cover`, then `lenny-test coverage --diff main --threshold 0.80 --require-gate` (`.claude/rules/git-workflow.md`, "v1 merge policy"; [change 0172](../archive/2026-11-26-0172-gate-speed/decisions.md), decisions 8 and 9). |
| Coverage profile | The `go test -coverprofile` file `tests/results/cover.out` that a gate run with `--cover` writes and whose SHA-256 its verdict records as `gate.cover_profile_sha256` (0172 decision 18). |
| Changed packages | The Go packages of the module whose non-test `.go` files the branch adds or changes against the gate's base, the files `lenny-test coverage --diff` measures. |
| Grant | The `RoleBinding` of the secret-probe fixture, which allows the Token Service user to `get` Secrets in the credentials namespace. |
| Held port | A loopback listener a test opens on port 0 and keeps open until the code under test takes it, so no other process can bind the port in between. |

## High-level design

The secret-probe test waits until the API server's authorizer allows the grant before its first probe, by asking the API server through a `SubjectAccessReview` until the answer is yes or a deadline passes. The stack tests hand the forwarder the listener they opened rather than its port number, through a seam in the package that the forwarder's two legs bind through. The Postgres fixture waits longer for a container under load, as open decision O1 settles.

The embedded-manifest check runs in the gate image before a code merge, as open decision O2 settles.

A gate run with `--cover` also builds the component and contract tests with coverage of the changed packages, and merges their profiles into the unit tier's profile before it hashes the profile. `lenny-test coverage --diff` then reads one profile that holds the lines any of the three tiers reached, under the hash and verdict checks of 0172 decisions 18, 24, and 25. Whether the gate adopts it depends on the time it adds, which open decision O3 settles after the measurement under "Validation owed" in `steps.md`.

## Requirements

None. The change edits test code, test fixtures, a test seam, and the merge gate's tooling.

## Current state

- `tests/component/secretprobe/secretprobe_envtest_test.go`: `startAPI` creates the `Role` and the `RoleBinding` and returns, and the test probes at once.
- `tests/testinfra/containers/postgres.go`: `PostgresOptions.withDefaults` sets `StartupTimeout` to 30 seconds; `SharedPostgres.start` uses the same default. `minio.go` and `fakegcs.go` default to 60 seconds.
- `pkg/embedded/stack/tlsproxy.go`: `startTLSProxy` and `startTCPRelay` call `net.Listen` on the address they are given. The tests' `freeLoopbackPort` (`bringup_test.go`) and `freePort` (`tlsproxy_test.go`) close their listener before returning the port.
- `cmd/lenny-test/cmd_gate.go`: `gateDocsFiles` lists `embedded_manifests_sync_test.go` among the general documentation gates, which the gate on `main` runs and the check before a code merge does not.
- `cmd/lenny-test/gate_restrict.go`: `prepareCoverProfile` turns coverage on for the unit tier's plan only; `runComponentTier` and `runContractTier` in `cmd/lenny-test/cmd_run.go` pass no coverage flags. `mergeCoverProfiles` in `cmd/lenny-test/gate_rerun.go` merges two profiles of one mode.

## Detailed design

### FT-1 The secret-probe grant

After `startAPI` creates the `RoleBinding`, it polls a `SubjectAccessReview` for the Token Service user, verb `get`, resource `secrets`, namespace `lenny-credentials`, through the admin client, until `status.allowed` is true, with a deadline of 30 seconds and a short interval. A deadline that passes fails the fixture with the last review's status. The test's cases are unchanged.

### FT-2 Held ports for the stack tests

`pkg/embedded/stack/tlsproxy.go` gains a package variable `listenTCP`, set to `net.Listen`, through which both forwarder legs bind. The package's tests replace it once for the test binary with a function that returns the held listener registered for the address, and otherwise calls `net.Listen`. A test helper opens a listener on `127.0.0.1:0`, registers it under its address, and returns its port; the test's cleanup closes a listener the forwarder did not take. The bring-up, lifecycle, forwarder, and relay tests use the helper for every port the forwarder binds. The relay test that needs an address with no listener keeps a released port, because it needs a refused dial. The seam follows the package's other seams (`createDevBearerSecretFn`, `applyEchoPoolFn`, and the others), and it changes no behavior of `lenny up`: the forwarder binds the same address through `net.Listen`.

### FT-3 The Postgres start timeout

As open decision O1 settles.

### EM-1 The embedded-manifest check in the gate image

As open decision O2 settles.

### CV-1 Coverage flags on the component and contract tiers

When a gate run has `--cover`, the gate lists the changed packages against the base the run compares with, keeps those that `go list` loads with the tier's build tag, and sets on the component and contract plans a coverage profile path and that package list. Each `go test` invocation of the two tiers then carries `-covermode=atomic`, `-coverpkg` with the list, and `-coverprofile` naming a file of its own, `tests/results/cover-<tier>-<n>.out`. A tier with no changed package to cover runs without coverage flags. The unit tier keeps its own flags, so a unit test measures its own package and the component and contract tests measure the changed packages wherever they reach them.

### CV-2 One profile

After the last phase, and before the gate hashes the profile (0172 decision 18), the gate merges each tier profile into `tests/results/cover.out` with `mergeCoverProfiles`, which sums the counts of a block that several profiles name, writes the result, and removes the tier profiles. A run whose unit tier wrote no profile starts from an empty profile of mode `atomic`. `prepareCoverProfile` removes tier profiles that an earlier run left. A tier profile that cannot be read or merged fails the tier that wrote it, so the verdict is not `PASS` and `coverage --diff` refuses the profile (0172 decision 24).

### CV-3 The verdict

The verdict records the tiers whose profiles the run merged as `gate.cover_tiers`, the run history carries them, and the note of `lenny-test coverage --diff` names them. The hash, the verdict, and the tier checks of 0172 decisions 18, 24, and 25 read the merged profile unchanged.

### CV-4 Reruns

`lenny-test gate --rerun-failed` keeps the original run's merged profile and merges the profile of the first rerun of failed unit tests into it, as it does today. A rerun of failed component or contract tests writes no profile, so the profile lacks the lines those tests would have reached after the point where they failed.

## Limits, and alternatives rejected

- CV-1 measures the changed packages only, which are the lines `coverage --diff` counts. A package the branch does not change is not instrumented in the component and contract binaries.
- CV-4 leaves the coverage of a rerun component or contract test out of the profile. A branch whose changed lines only such a test reaches reads them as uncovered and reruns the gate.
- FT-2 keeps one released port, the dead upstream of the relay test, because that test needs a dial that is refused. A process that binds the port in between makes the test's dial succeed and its read time out, and the test fails.
- The alternative to FT-2 that the issue names, letting the forwarder bind port 0 and report its address, changes what `HTTPPort` and `HTTPSPort` of 0 mean in `lenny up`, where 0 means the documented 8080 and 8443.

## Specification and non-specification changes

- Specification: none.
- Tests and fixtures: `tests/component/secretprobe/secretprobe_envtest_test.go`; `pkg/embedded/stack/bringup_test.go`, `lifecycle_test.go`, `stack_test.go`, `tlsproxy_test.go`, and `testsupport_test.go`; `tests/testinfra/containers/postgres.go` and `postgres_shared.go` (O1).
- Code: `pkg/embedded/stack/tlsproxy.go` (the `listenTCP` seam).
- Merge gate: `cmd/lenny-test/` (CV-1 to CV-3, and EM-1 under O2), and the placement of `embedded_manifests_sync_test.go` under O2.
- Records: `TESTING.md` ("Restricted runs and coverage" and "Verdict"), `cmd/lenny-test/README.md`, and, once O3 is settled, the rule texts that say `--cover` writes the unit run's profile (`.claude/rules/git-workflow.md`, "v1 merge policy", and `.claude/rules/test-coverage.md`, "How to run").

## Documentation

No reader-facing behavior changes: the change touches no specification page. The pages for contributors are `TESTING.md` and `cmd/lenny-test/README.md`, which state the `--cover` profile and, under O2, where the embedded-manifest check runs.

## Tests

- FT-1: the component test itself, run repeatedly in the component tier.
- FT-2: the unit tests of `pkg/embedded/stack`, run repeatedly, and a unit test of the held-listener helper: a held port is handed to the forwarder, and a port no test holds is bound through `net.Listen`.
- FT-3: the component tests that start Postgres, under O1.
- CV-1 to CV-3: unit tests of `cmd/lenny-test`: the coverage flags of each tier's invocations with and without `--cover`, the changed-package list, the merge of tier profiles into the unit profile, a merge failure that fails the tier, the removal of stale tier profiles, and `gate.cover_tiers` in the verdict and in the note of `coverage --diff`. A gate run with `--cover` on a branch whose changed lines only component tests reach shows them covered.
