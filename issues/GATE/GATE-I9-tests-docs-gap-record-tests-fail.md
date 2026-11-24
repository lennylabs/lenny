---
id: GATE-I9
status: open
kind: defect
severity: medium
requirements: []
---

# The gap-record reconciliation tests in `tests/docs` fail on `main`

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. Change 0172 decision 16 records it.

## Evidence

- `CGO_ENABLED=0 go test -count=1 ./tests/docs/...` on `main` at `1a39e852d` on 2026-11-24 fails 18 top-level tests, with 102 failing tests and subtests in all. Among them are `TestClosedStreamFindingsCiteLandedCodeAndTests_spec_7_3`, `TestReleaseOwnershipFiledFindingsAreFiledOnce`, `TestSessionTimersRecordsResolveTheirMembers_spec_6_2`, and `TestTerminalPipelineRecordsCloseTheirMembers_spec_7_1`.
- Gate run `d47d33b8ed80054999c769e1` (`lenny-test gate --all`, 2026-11-24) reports the same failures in its unit tier, which runs `go test ./...`.
- A selected gate run places `tests/docs` in the docs tier and runs only that tier's general gates, so no selected run on `main` has run these tests.

## Gap

The tests compare the gap records with the code and the tree, and the records or the tree have moved since the tests were written. They fail on every run that includes them, so they verify nothing while they stay.

## Suggested resolution

Fix each test against the current records, or retire it where the area work that replaces the prose-matching documentation tests ("Documentation checks" in `.claude/rules/test-coverage.md`) covers what it checked. Step 0172.S7 leaves `tests/docs` out of a full run's unit tier in the meantime (decision 16).
