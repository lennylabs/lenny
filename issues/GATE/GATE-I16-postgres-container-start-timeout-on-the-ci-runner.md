---
id: GATE-I16
status: open
kind: test-gap
severity: low
requirements: []
---

# Postgres test containers time out at start on the gate-main runner

## Specification

None: the defect is in the test infrastructure, `tests/testinfra/containers/postgres.go`, and the gate-main workflow of change 0172 decision 27. The issue is filed under `GATE`.

## Evidence

- Gate-main workflow run `45872051820` (selected run on `main` at `0384658de`, 2026-11-26, gate run `6e7ecf0201404efd760423b3`): seven component tests in `tests/component/auditintegrity` and `tests/component/auditstore` failed at `StartPostgres` with "wait until ready: \"database system is ready to accept connections\" matched 0 times, expected 2 ... context deadline exceeded" after 30 seconds. The tests run in parallel (`t.Parallel`), and each starts its own container. The previous run, `45869562906` at `ff5c50644`, whose code is the same, passed all seven. The rerun of `45872051820` (attempt 2) on the same commit passed the whole gate, so the failure is a load failure that a rerun does not reproduce.
- `StartPostgres` waits at most 30 seconds by default (`opts.StartupTimeout`), where the MinIO and fake GCS containers wait 60 seconds. The workflow's runner has 4 vCPUs.

## Gap

On the 4-vCPU runner, a burst of parallel container starts can take longer than 30 seconds, and the gate on `main` fails on a load failure that a rerun clears, which costs about an hour per rerun.

## Suggested resolution

Raise the Postgres start timeout to the 60 seconds the other containers use, or bound the number of containers a component package starts at once, and confirm on the next runs of the workflow that the timeout no longer recurs.
