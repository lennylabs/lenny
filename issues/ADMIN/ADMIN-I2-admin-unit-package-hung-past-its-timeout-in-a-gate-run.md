---
id: ADMIN-I2
status: open
kind: test-gap
severity: low
requirements: []
---

# The unit tests of pkg/gateway/api/admin hung past the package timeout in one gate run

## Specification

None. The finding is about a test of the package, whose tests cite §15.1 and the admin sections of the specification.

## Evidence

- Local gate run `400177a9d61814a63a3f6af4` on `fecc6a961` (branch `change/0177.S3-S4-audit-tail-and-key-incidents`, 2026-11-27, gate image `lenny-gate:7d2b778c8f53`, unit tier with `-race` and coverage): `FAIL github.com/lennylabs/lenny/pkg/gateway/api/admin 1800.245s`, the package's test timeout; the unit tier took 31 minutes. The verdict lists the package and no test name, so the test that hung was not captured.
- The same package passed on the host without `-race` in 67 seconds on the same commit, and in the gate image with `-race` through `lenny-test run --pkg pkg/gateway/api/admin --rdeps-depth 0 --tier unit --container` in about 6 minutes.
- Gate run `17dab7ad64c36a9ae38d1bc7` on `aee6e7c23`, whose changes from `fecc6a961` are comments in `pkg/signingkeys/ring.go`, `pkg/gateway/api/admin/upload_token_keys.go`, and `upload_token_keys_test.go`, passed the unit tier in 6 minutes 53 seconds.
- The merge of `main` at `07cad2fc0` into the branch brought changes to `pkg/gateway/api/admin/drain.go` and `drain_test.go`.

## Gap

A test of the package can wait without bound, which a fixed wait or an unclosed channel under `-race` scheduling would explain. The gate's verdict records a package timeout without the goroutine dump that names the test.

## Suggested resolution

Run the package repeatedly in the gate image with `-race` and `-timeout 10m` (for example `-count=20`) to reproduce the hang and read the goroutine dump, then fix the wait. Separately, record the running test names of a package that times out in the gate's verdict.
