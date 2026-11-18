---
id: TOKX-I1
status: open
kind: test-gap
severity: medium
requirements: []
---

# The cross-replica impersonation test fails intermittently under load

## Specification

§13.3 (Credential Flow) states the impersonation session's `exp` check, which the test's `expiry` subtest exercises with one second of allowed skew. The token-exchange page has not landed, so the requirement is cited by its numbered section.

## Evidence

- `TestImpersonationSessionIsSharedAcrossReplicas_spec_13_3` in `tests/integration/impersonation_revocation_cross_replica_test.go` passed 6 of 7 runs and failed once, with its `expiry` subtest, on one commit of `main` (`343c04eb7`), while other gate runs shared the host (0137.S5 build, 2026-11-17).
- The same test and subtest failed in gate run `f03de65f998134e1bc5016c9` and in the first gate run of 0117.S1-S2 (`787cda69e328b9967c012f63`), both under host load, and passed on rerun.
- The test is quarantined in `tests/flake-budget.yaml` from 2026-11-17 and skips with a reason in the `quarantined:` category.

## Gap

The test depends on wall-clock timing across two gateway replicas: it starts a 60-second session, runs other cases while the session approaches its `exp`, and then sleeps until `exp` plus two seconds before asserting a 401 on both replicas. Under host load the replicas, Redis, or Postgres respond slowly enough that an assertion lands on the wrong side of a deadline, so the merge gate fails without a defect in the code under test.

## Suggested resolution

Make the test independent of wall-clock load: inject the replicas' clock, or poll each assertion until a bounded deadline derived from the session's `exp` rather than sleeping a fixed interval. Confirm with `lenny-test stress --test TestImpersonationSessionIsSharedAcrossReplicas_spec_13_3 --runs 50` under load, then remove the quarantine entry and the skip.
