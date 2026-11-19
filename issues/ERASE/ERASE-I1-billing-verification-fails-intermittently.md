---
id: ERASE-I1
status: open
kind: test-gap
severity: medium
requirements: []
---

# The erasure job's billing verification fails intermittently

## Specification

§12.8 requires the erasure job to verify, after pseudonymizing the user's billing events, that neither the erasure salt nor the original user id survives, and to fail the job when the check does not pass.

## Evidence

- Merge gate run `5fe88e7f64fb9c324fbd3653` (branch `change/0117.S4-seam-model`, commit `8d75a6144`), integration tier: `TestErasureJobJourney/a_legal_hold_blocks_erasure_and_a_platform-admin_override_proceeds` failed with `override erasure job phase = failed, want completed`, the job's error being `billing erasure verification failed: the erasure salt or the original user id survived pseudonymization` (`pkg/gateway/storage/erasurejob/runner.go`, `errBillingVerification`).
- The same test passed 4 of 4 runs on that commit and 4 of 4 on `main` (`c9e094733`) on a quiet host, and in every earlier gate run. The failing run overlapped other docker-backed tests on the host.
- The test runs with `t.Parallel()` against a shared datastore.

## Gap

Either a billing event for the erased user is written after pseudonymization and before verification (a race in the job or in the billing writer), or another parallel test writes billing rows the verification reads. The first is a product defect under §12.8; the second is a test isolation gap. A merge gate failure on this test costs a full rerun.

## Suggested resolution

Find which: check whether any billing write for a session of the user can land after the pseudonymization phase starts (the post-terminal window of §7.1 is the candidate), and whether the verification query is scoped to the job's tenant and user. Fix the race or scope the test's data, and add a test that drives a late billing write during the job.
