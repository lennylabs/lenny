---
id: GATE-I14
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0181.S4
---

# `coverage --diff` measures only the unit tier, so code that component tests cover reads as uncovered

## Specification

None: the defect is in the merge gate's coverage check, which `.claude/rules/test-coverage.md` ("Coverage target") and change 0172 decisions 8, 18, and 23 to 25 define. The issue is filed under `GATE`.

## Evidence

- `lenny-test gate --cover` writes the coverage profile of the unit tier alone (change 0172 decision 9), and `lenny-test coverage --diff` reads that profile.
- Step 0176.S1 (2026-11-25, gate run `cc7d4399f9e28c53f221834b`): `coverage --diff --require-gate` reported 56 of 79 changed lines (70.9%). Twenty of the 23 uncovered lines are column handling in a Postgres store (`pgstore.go`), which the unit tier cannot reach because it has no Postgres, and which the step's component, integration, and security tests exercise.

## Gap

A change to a Postgres store, a controller, or any code that only the component tier and later tiers reach fails the 80% floor although its tests run, and the only remedy is a waiver for code that is not wiring.

## Suggested resolution

Write coverage profiles for the component and contract tiers of the restricted gate run as well (`go test -coverprofile` with `-coverpkg` over the changed packages), merge them with the unit profile, and let `coverage --diff` read the merged profile, under the hash and verdict checks of decisions 18, 24, and 25. Measure the added time before adopting it.
