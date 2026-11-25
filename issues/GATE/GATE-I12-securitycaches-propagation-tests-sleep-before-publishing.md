---
id: GATE-I12
status: open
kind: test-gap
severity: low
requirements: []
---

# The credential denylist and revocation propagation tests wait a fixed 100 ms for their subscribers

## Specification

None: the defect is in the tests of `tests/component/securitycaches`, which verify the cross-replica propagation of revocations and credential denylist entries. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the checks of each tier.

## Evidence

- `tests/component/securitycaches/propagation_test.go` starts the subscribers' run loops and then sleeps a fixed `100 * time.Millisecond` before publishing (three sites), with the comment that a pub/sub message published before `SUBSCRIBE` completes is dropped.
- Gate run `e16818ce8507b5fcd52e7008` (branch `change/0171.S2-0172.S10`, commit `256976e98`, 2026-11-25): `TestCredentialDenylistPropagation/a_pool-backed_revocation_on_replica_A_reaches_replica_B` timed out. Gate run `a1c37cf2862994aa16ea5843` on the same commit passed, and the test passed five more times on the host. The branch does not change the code the test exercises.

## Gap

Under load the subscribers can take longer than 100 ms to confirm their subscriptions, so the publish is dropped and the test times out without a defect in the code. The same fixed-pause pattern caused the `TestPythonClientStreamingReconnect` failures that `fix/sdk-reconnect-probe-wait` fixed on 2026-11-25.

## Suggested resolution

Wait for each subscriber's subscription confirmation before publishing, for example through the `OnSubscribed` callback that change 0171.S2 adds to `pkg/storage/pubsub`, and drop the fixed sleeps.
