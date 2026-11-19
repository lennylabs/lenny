---
id: RTPSDK-I1
status: open
kind: test-gap
severity: medium
requirements: []
---

# The Go SDK's overlapping-creations release test fails intermittently

## Specification

§28.5.3 (`CH-MSGSOCK` inbound `session_end`) and §15.7 (Runtime Author SDKs): a `session_end` that arrives while a session's creation is still running ends that creation, which the SDK releases when it completes, and a later `session_start` for the same session builds a new context; each creation's context is released exactly once.

## Evidence

- `tests/contract/sdks/runtime_sdk_sessions_test.go`, `TestRuntimeSDKOverlappingCreationsAreEachReleasedOnce_spec_28_5_3/go`, failed 1 of 10 and 2 of 20 runs on `main` at `fd4ea4def`, and once in a full package run on the 0117.S6 branch, with `releases = [released_sess_a_1], want creation 1's context released first and creation 2's second`, the event log showing `create_sess_a_2` and a second `terminate_sess_a_session_end` but no `released_sess_a_2` at the assertion.
- The test is quarantined in `tests/flake-budget.yaml` (2026-11-19).

## Gap

Either the test asserts before the second creation's release, which completes asynchronously, has been recorded (a test gap), or the Go SDK releases the second overlapping creation late or not at all (a defect under §28.5.3).

## Suggested resolution

Make the test wait, with a deadline, for the expected release events before asserting. If the second release never arrives within the deadline, fix the Go SDK's release of an overlapping creation. Then remove the quarantine entry and the skip.
