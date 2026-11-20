---
id: AUDIT-I1
status: open
kind: test-gap
severity: medium
requirements: []
legacy: T-12.9.18
---

# The audit sequence security test hung in the gateway probe bootstrap on the July cluster

## Specification

§11.7 (Audit Logging) governs the audit sequence that the security-tier test `TestAuditSequenceMonotonicity` checks. The finding concerns the test infrastructure that reaches the cluster, so a hang removes the test's verdict on that requirement.

## Evidence

- Recorded on branch `section-26` as T-12.9.18. `TestAuditSequenceMonotonicity` in `tests/security/audit_integrity_test.go` blocked inside `startGatewayProbe` (`tests/security/gateway_probe_test.go`), in the `kubectl wait --for=condition=Ready ... --timeout=120s` call, and the package timed out before the test generated any audit event.
- `Cluster.Kubectl` in `tests/testinfra/kind/kind.go` builds the command with `exec.Command` and no context deadline, and `Cluster.KubectlOut` in `tests/testinfra/kind/install.go` waits on it without a bound. The finding's reproduction showed `kubectl wait` outliving its own `--timeout`, which points at the July cluster or the kubectl build.
- No result file under `tests/results` records a security-tier run of this test. `TEST-GAPS.md` T-12.9.14 and T-12.9.16 record the same test failing later with "0 rows dropped", which is a distinct symptom.
- Checked against `main` at `092ef92f6` on 2026-11-19. The finding cannot be confirmed or dismissed without a run.

## Gap

If the hang reproduces, the test never reaches its audit assertions, and the package timeout removes the verdict of the whole security package rather than one test's. Whether it reproduces on the current tree is unconfirmed, and a validation run settles whether this issue stays open.

## Suggested resolution

In the validation pass that runs the security tier for M3, run `go test -tags security -run '^TestAuditSequenceMonotonicity$' ./tests/security/` against the Kind cluster. If it hangs, give the `Cluster.Kubectl` callers a context deadline (`exec.CommandContext`) as a plain commit and close this issue with it. If it completes, close this issue as obsolete with the run identifier.
