---
id: GATE-I1
status: open
kind: build-gap
severity: low
requirements: []
---

# The tree carries golangci-lint findings that keep the whole-tree lint a warning

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. It was filed as CONV-I1 and moved to this code on 2026-11-19, by the owner's answer to GT-1 of the review of the changed-line lint. The rule in question is in `.claude/rules/code-best-practices.md` ("How to apply when editing"): "Run `gofumpt`, `goimports`, and the static tier; resolve every linter finding rather than suppressing it."

The owner settled question S-4 of the M3 readiness review on 2026-11-19 with option (b): golangci-lint findings on the lines a branch changes fail the merge gate now, and the existing findings stay a warning.

## Evidence

- `cmd/lenny-test/lint_changed.go` runs golangci-lint twice in the static tier. The run with `--new-from-patch`, over the diff of the working tree against the branch's merge base, fails the tier on a finding. The whole-tree run reports its findings as a non-fatal warning that names this issue.
- `GOOS=linux CGO_ENABLED=0 golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` (golangci-lint 2.12.2, the release `tests/gate/Dockerfile` and `scripts/lib/versions.sh` pin) over `main` at `db6c42d36`, with `.golangci.yml` and no build tags, reports 353 findings in 88 packages. The gate's own run keeps golangci-lint's default caps of 50 findings per linter and 3 findings with the same text, so its warning lists fewer.
- Without `GOOS=linux`, on a darwin host, the run reports one type-checking error in `tests/testinfra/supervisedpod`, which builds only on Linux, and no other finding. The check therefore analyzes the Linux build on a host other than Linux. The counts below are from the Linux build, which is the gate's environment.

Findings per linter:

| Linter | Findings |
|:--|--:|
| `staticcheck` | 222 |
| `errcheck` | 100 |
| `unused` | 16 |
| `misspell` | 7 |
| `ineffassign` | 6 |
| `govet` | 2 |
| Total | 353 |

The `staticcheck` findings by check: `SA1019` (use of a deprecated identifier) 67, `SA1012` (a nil `context.Context`) 51, `QF1008` 29, `SA4006` 13, `QF1001` 9, `ST1023` 8, `SA4023` 10 (4 findings and 6 related-information entries that golangci-lint reports separately), `ST1005` 5, `S1016` 5, `QF1006` 4, `QF1011` 4, `SA4000` 4, `ST1011` 3, `S1024` 2, `SA5011` 2 (1 finding and 1 related-information entry), and 1 each of `SA1008`, `SA9003`, `QF1003`, `QF1002`, `SA4010`, and `SA4017`.

Findings per package and linter:

| Package | `errcheck` | `govet` | `ineffassign` | `misspell` | `staticcheck` | `unused` | Total |
|:--|--:|--:|--:|--:|--:|--:|--:|
| `pkg/gateway/platformtools/mcp` | 0 | 0 | 0 | 0 | 48 | 1 | 49 |
| `pkg/gateway/api/admin` | 0 | 0 | 0 | 0 | 43 | 0 | 43 |
| `pkg/ops/events` | 36 | 0 | 0 | 0 | 1 | 1 | 38 |
| `pkg/workspaceplan` | 22 | 0 | 0 | 0 | 0 | 0 | 22 |
| `pkg/gateway/core/session/sessionserver` | 0 | 0 | 0 | 0 | 15 | 0 | 15 |
| `pkg/loadctl` | 0 | 0 | 0 | 0 | 9 | 0 | 9 |
| `cmd/lenny-test` | 3 | 0 | 2 | 0 | 2 | 1 | 8 |
| `pkg/platform/store` | 0 | 0 | 0 | 0 | 8 | 0 | 8 |
| `pkg/controller/agentpod/podspec` | 0 | 0 | 0 | 0 | 7 | 0 | 7 |
| `pkg/controller/warmpool` | 0 | 0 | 0 | 0 | 7 | 0 | 7 |
| `pkg/credential/credentialserver` | 0 | 0 | 0 | 0 | 7 | 0 | 7 |
| `pkg/billing/correctionstore` | 6 | 0 | 0 | 0 | 0 | 0 | 6 |
| `pkg/ops/opsidem` | 6 | 0 | 0 | 0 | 0 | 0 | 6 |
| `pkg/ops/opsserver` | 1 | 1 | 2 | 0 | 2 | 0 | 6 |
| `tests/testinfra/inproc` | 5 | 0 | 0 | 0 | 0 | 1 | 6 |
| `cmd/lenny-gateway` | 0 | 0 | 0 | 0 | 5 | 0 | 5 |
| `pkg/storage/partitionmaint` | 0 | 0 | 0 | 0 | 4 | 1 | 5 |
| `pkg/controller/poolscaling` | 0 | 0 | 0 | 4 | 0 | 0 | 4 |
| `pkg/ctlcli` | 0 | 0 | 0 | 3 | 0 | 1 | 4 |
| `pkg/podlifecycle` | 0 | 0 | 0 | 0 | 4 | 0 | 4 |
| `pkg/adapter` | 0 | 0 | 0 | 0 | 3 | 0 | 3 |
| `pkg/clockinject` | 0 | 0 | 0 | 0 | 2 | 1 | 3 |
| `pkg/gateway/runtime/tenantaffinity` | 3 | 0 | 0 | 0 | 0 | 0 | 3 |
| `pkg/ops/opsservice` | 1 | 0 | 0 | 0 | 2 | 0 | 3 |
| `pkg/pgwritemetrics` | 3 | 0 | 0 | 0 | 0 | 0 | 3 |
| `tests/testinfra/kind/reinstall` | 0 | 0 | 0 | 0 | 3 | 0 | 3 |
| `cmd/lenny-adapter` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/controller/controllermetrics` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/gateway/core/podclaim` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/gateway/core/session/watchdog` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/gateway/seam` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/loadrunner/dispatch` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/ops/backup` | 0 | 0 | 0 | 0 | 1 | 1 | 2 |
| `pkg/redisconn` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/storage/failopen` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `pkg/tenantkms` | 2 | 0 | 0 | 0 | 0 | 0 | 2 |
| `pkg/tokenservice` | 0 | 0 | 0 | 0 | 1 | 1 | 2 |
| `pkg/upload/archive` | 0 | 0 | 0 | 0 | 2 | 0 | 2 |
| `sdks/client/go/lenny` | 2 | 0 | 0 | 0 | 0 | 0 | 2 |
| `sdks/client/go/test-helper` | 2 | 0 | 0 | 0 | 0 | 0 | 2 |
| `pkg/adapter/statelessslot` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/api/openapi` | 0 | 1 | 0 | 0 | 0 | 0 | 1 |
| `pkg/audit/auditstore` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `pkg/billing/billingstore` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/billing/billingstore/failover/redisstream` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/blobstore/s3` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `pkg/controller/cidrdrift` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/controller/tenantdeletion` | 0 | 0 | 1 | 0 | 0 | 0 | 1 |
| `pkg/gateway/api/ratelimit` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/api/wsbearer` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/checkpoint/checkpointer` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/checkpoint/resumechunks` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/coordination/dualstore` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/coordination/leasestore` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `pkg/gateway/core/leaderjobs` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/session/budgetstate` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/session/recycle` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/session/routingcache` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/gateway/core/session/sessioninbox` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/session/slotcounter` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/core/session/slotreaper` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/credentials/credassign` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `pkg/gateway/drain/prestop` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/experiment/experimentprovider` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/platformtools/delegation` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/platformtools/mcptools` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/platformtools/toolapproval` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/gateway/playground` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `pkg/gateway/replica/gatewayleader` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/seam/subsystem` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/gateway/streamproxy` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/mtls/interceptordial` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/ops/diagnostics/prodsource` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/ops/eventsubscription/pgstore` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/ops/operations` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/ops/opsidem/pgstore` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `pkg/recommendations` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/runtimekit/echocore` | 0 | 0 | 1 | 0 | 0 | 0 | 1 |
| `pkg/storage/erasurejob/saltlockpg` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `pkg/tenancy/tenantstore` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `scripts/specshift/annotation` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `scripts/specshift/survey` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `tests/testinfra/commitfault` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/gateway` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `tests/testinfra/kind` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/loadgen` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/opsprocess` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `tests/testinfra/sessiondriver` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| Total | 100 | 2 | 6 | 7 | 222 | 16 | 353 |

## Gap

The whole-tree golangci-lint run of the static tier stays non-fatal while these findings remain, so a finding outside a branch's changed lines does not block a merge. A branch that edits a line carrying one of these findings has to fix it, because the changed-line run reports it.

The counts cover the default build configuration. Files behind a build tag, such as the `integration`, `component`, and `contract` tests, are outside both runs.

## Suggested resolution

Fix the findings package by package, starting with the packages that carry the most, and recount with the command under "Evidence". When the count reaches zero, make the whole-tree run in `cmd/lenny-test/lint_changed.go` fail the tier, remove the changed-line run, whose findings the whole-tree run then covers, update item 7 of the static-tier check list in `TESTING.md`, and close this issue.
