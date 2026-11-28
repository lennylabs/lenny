---
id: GATE-I19
status: open
kind: test-gap
severity: low
requirements: []
---

# Redis test containers time out at start, and MinIO at stop, under host load

## Specification

None: the defect is in the test infrastructure, `tests/testinfra/containers/redis.go` and `minio.go`. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the test harness, as GATE-I16 is for the same failure in the Postgres fixture.

## Evidence

- The local gate on the owner's workstation (2026-11-27): `lenny-test gate --tier static,unit,component,contract --cover` run `f0d708a51a8353bfa58154a0` on `9bc02ae76` (branch `change/0181.S2-postgres-start-timeout`, `main` `4d54cd4d3` merged in) failed ten component tests and subtests. Eight failed at `StartRedis` after 30 seconds with "start container: container start: Post \"http://%2Fvar%2Frun%2Fdocker.sock/v1.54/containers/<id>/start\": context deadline exceeded": `TestOpsEventStreamPollResolvesCursorBeyondDefaultStreamLength` (`tests/component/eventstream`); `TestCredentialDenylistPropagation`, `TestMTLSDenylistPropagation`, `TestTokenRevocationMissedBeforeTheFirstSubscriptionIsAppliedWhenItIsConfirmed`, `TestTokenRevocationMissedDuringARedisLinkLossIsAppliedOnReconnect`, and `TestTokenRevocationPropagation` (`tests/component/securitycaches`); `TestStorageQuotaCounterContract` (`tests/component/storagequota`); and `TestBudgetScriptsSentinelAndTTLRulesOnRealRedis_spec_8_3` (`tests/component/treebudget`). `TestObjectStoreCheckerAgainstMinIO/reports_unhealthy_and_stamps_MINIO_UNREACHABLE_when_MinIO_is_down` (`tests/component/healthchecks`) failed at `MinIO.Stop` after 10 seconds with "terminate: stop: container stop: Post \".../containers/<id>/stop?t=10\": context deadline exceeded", and its parent test failed with it. The host's load average was about 16 when the run ended, with Go compiles of other sessions running outside the gate lock.
- `lenny-test gate --rerun-failed` run `fd20ea4ef4eb05c31b161aca` on the same commit passed each of the failed tests 3 of 3. The previous gate run on the same tree, `410b9e99ae815f9ce21ca1ff` on `c3d2bbd8f` (the same branch before `main` was merged in, a merge that changed no file), passed every component test. The failure is a load failure that a rerun does not reproduce.
- `StartRedis` waits at most 30 seconds by default (`RedisOptions.StartupTimeout`), and that bound also covers the Docker API calls that create and start the container. `MinIO.Stop` waits 10 seconds (`tests/testinfra/containers/minio.go`), as do the cleanup terminations of the Redis and MinIO fixtures. The Postgres fixture's start timeout became 60 seconds in step 0181.S2 (change 0181, decision 1), and the MinIO and fake GCS fixtures start with 60 seconds.
- The securitycaches propagation tests have an open issue of their own, GATE-I12, for a fixed wait before publishing; the failures above are at the Redis start, before that wait.

## Gap

Under a burst of parallel container starts on a loaded host, a Docker API call can take longer than 30 seconds to start a Redis container, or longer than 10 seconds to stop a MinIO container, and the gate fails on a load failure that a rerun clears.

## Suggested resolution

Raise the Redis start timeout to the 60 seconds of the other fixtures, and the stop timeouts of `MinIO.Stop`, `Redis.Stop`, and the fixtures' cleanup terminations to a bound that a loaded Docker daemon meets, such as 30 seconds; confirm on the next gate runs that the timeouts no longer recur. The owner decides whether this joins change 0181 as a step beside 0181.S2.
