---
id: RTPSDK-I2
status: open
kind: test-gap
severity: low
requirements: []
---

# The Python runtime SDK's lifecycle contract check times out under host load

## Specification

The runtime SDK lifecycle contract that `TestRuntimeSDKLifecycleFullLevelInterpreted` in `tests/contract/sdks/runtime_sdk_test.go` exercises for each interpreted SDK.

## Evidence

- The local gate on the owner's workstation (2026-11-27): `lenny-test gate --tier static,unit,component,contract --cover` run `ea0b1769f6b798b06137b625` on `43affeca8` (branch `change/0178.S3-acting-party`) failed `TestRuntimeSDKLifecycleFullLevelInterpreted/python` with `check "deadline_signal_handling" failed: runtime did not connect to the lifecycle socket within deadline`, while another session's `e2e.test` held the host at a load average above 40.
- `lenny-test gate --rerun-failed` run `f3e73c9111e689fe913c442d` on the same commit passed it, and run `4c02b1666b67191a1a8c70ed` passed it again and recorded `PASS`.
- The branch does not touch the runtime SDKs.

## Gap

The check's deadline for the Python runtime to connect to the lifecycle socket does not hold under host load, so the contract tier fails on a load failure that a rerun clears.

## Suggested resolution

Wait for the runtime's connection as an event with a deadline scaled to the environment, or raise the deadline, and confirm on the next gate runs that it no longer recurs.
