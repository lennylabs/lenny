---
id: LOCALDEV-I2
status: planned
kind: test-gap
severity: low
requirements: []
change: 0181.S1
---

# The embedded stack's bring-up tests release the ports they reserve before the stack binds them

## Specification

None: the defect is in the tests of `pkg/embedded/stack`. The issue is filed under `LOCALDEV`, the code of the local development stack.

## Evidence

- `pkg/embedded/stack/bringup_test.go`: `freeLoopbackPort` opens a listener on `127.0.0.1:0`, reads its port, and closes it; `upConfig` passes two such ports as `HTTPPort` and `HTTPSPort`, and `Up` binds them later (`tlsproxy.go`, "embedded http-relay: listen on").
- Gate run `e1eb9883c44df9d18fea46c6` (branch `change/0176.S1-pipeline-mark`, commit `f4464a2a2`, 2026-11-26): `TestUpWarmReconcileSkipsReapplyOnTagMatch_spec_17_4` failed with "listen tcp 127.0.0.1:56909: bind: address already in use". The branch does not touch `pkg/embedded`. Its `lenny-test gate --rerun-failed` run `d0b76fb7f7a807c81cf6267a` on the same commit passed the test 3 of 3.
- Gate run `a63d1fdbcf3a1a9c0492aea6` (branch of 0171.S1, 2026-11-24): `TestUpWarmReconcileReappliesOnTagMismatch_spec_17_4` failed with the same error and passed 10 of 10 reruns.

## Gap

Between the release of the reserved port and the stack's bind, another test process of the same gate run can take the port, so the tests fail without a defect in the code. Each failure costs a rerun.

## Suggested resolution

Let the stack bind port 0 and report the bound address, or pass the test's open listener into the stack, so no port is released and reused. Drop `freeLoopbackPort` from the bring-up tests.
