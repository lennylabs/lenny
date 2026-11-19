---
id: OPSEV-I1
status: open
kind: question
severity: medium
requirements: []
legacy: [T-25.5.27, T-12.9.18]
---

# Findings from July cluster runs need a run on the current tree before they can be judged

## Specification

§25.5 (Operational Event Stream), **Redis unreachable:**: "SSE, polling, and webhook delivery fall back to the gateway's in-memory event buffer (Section 25.3). Responses include the canonical `degradation` envelope with `actualSource: "gateway-buffer"`."

§11.7 (Audit Logging) governs the audit sequence that the security-tier test `TestAuditSequenceMonotonicity` checks. That finding concerns the test infrastructure rather than a requirement, and this issue holds it with the §25.5 finding so both reach the validation pass.

## Evidence

- T-25.5.27, recorded on branch `theme-t-bed`. On the July Kind cluster, `lenny-ops` switched its read surface to `gateway-buffer` during a Redis outage and returned zero events from either gateway replica, so the degradation label sat on an empty page. The test is `TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey` in `tests/e2e_kind/event_buffer_fanout_test.go`. Since 2026-07-25 the fan-out code in `pkg/ops/events/gatewayfallback.go` and `pkg/ops/gateway/client.go` has changed only through package moves and citation rewrites. The test registers four precondition skips in `tests/registers/skip-reasons.yaml`, so a run that skips proves nothing.
- T-12.9.18, recorded on branch `section-26`. `TestAuditSequenceMonotonicity` in `tests/security/audit_integrity_test.go` blocked inside `startGatewayProbe` (`tests/security/gateway_probe_test.go`), in the `kubectl wait --for=condition=Ready ... --timeout=120s` call, and the package timed out before the test generated any audit event. `Cluster.Kubectl` in `tests/testinfra/kind/kind.go` builds the command with `exec.Command` and no context deadline, and `Cluster.KubectlOut` in `tests/testinfra/kind/install.go` waits on it without a bound. The finding's reproduction showed `kubectl wait` outliving its own `--timeout`, which points at the July cluster or the kubectl build. No result file under `tests/results` records a security-tier run of this test. `TEST-GAPS.md` T-12.9.14 and T-12.9.16 record the same test failing later with "0 rows dropped", which is a distinct symptom.
- Both findings were checked against `main` at `092ef92f6` on 2026-11-19. Neither can be confirmed or dismissed without a run.

## Gap

If T-25.5.27 reproduces, the §25.5 degraded read path labels a response `gateway-buffer` while serving none of the events the gateway buffered, which is a product defect. If T-12.9.18 reproduces, a hang in the probe bootstrap removes the verdict of the whole security package rather than one test's.

## Suggested resolution

Settle both in the validation pass that runs the e2e_kind and security tiers for M3.

- Run `TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey` unskipped on a two-replica Kind install. If it fails, refile it as an OPSEV build gap with the run's output. If it passes, close this part as obsolete.
- Run `go test -tags security -run '^TestAuditSequenceMonotonicity$' ./tests/security/` against the Kind cluster. If it hangs, give the `Cluster.Kubectl` callers a context deadline (`exec.CommandContext`) as a plain commit. If it completes, close this part as obsolete.

Close this issue when both parts are settled.
