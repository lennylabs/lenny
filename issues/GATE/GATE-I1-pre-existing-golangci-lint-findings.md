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

On 2026-11-20 the owner kept one rule set for test files and other code (GT-6), so the findings in test files are fixed under this issue rather than excluded by a path rule in `.golangci.yml`, and kept the register as counts per package and linter (GT-13), so a branch that removes one finding and adds another of the same linter in the same package off its changed lines passes.

## Evidence

- `cmd/lenny-test/lint_changed.go` runs golangci-lint twice in the static tier. The run with `--new-from-patch`, over the diff of the working tree against the branch's merge base, fails the tier on a finding. The whole-tree run counts its findings per package and linter and compares the counts with the register `tests/registers/golangci-lint-findings.yaml`: a count above the register fails the tier, and the findings within it are a non-fatal warning that names this issue (owner's answer to GT-3, 2026-11-19).
- `GOOS=linux CGO_ENABLED=0 golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` (golangci-lint 2.12.2, the release `tests/gate/Dockerfile` and `scripts/lib/versions.sh` pin) over `main` at `db6c42d36`, with `.golangci.yml` and no build tags, reports 353 findings in 88 packages. The same command over `main` at `b5e4cab8f` reports the same 353 findings, and the register was first seeded from that run.
- Both runs of the check set the build tag of every tier and the `smoke` tag (owner's answer to GT-4, 2026-11-19). The same command with `--build-tags=chaos,component,conformance,contract,e2e_cloud,e2e_kind,integration,load_cloud,load_kind,load_local,security,smoke` over the same tree reports 377 findings in 102 packages. The 24 findings the tags add are all in test files under `tests/`: `errcheck` 11, `staticcheck` 7, `unused` 4, `ineffassign` 1, and `misspell` 1. The register carries them.
- `.golangci.yml` reads a file as generated only when it carries the standard `// Code generated ... DO NOT EDIT.` header (`generated: strict`), and it enables `nolintlint` with `require-specific` and `require-explanation` (owner's answer to GT-5, 2026-11-19). With both, the run over the same tree and tags reports the same 377 findings: each of the 14 `//nolint` directives in the tree names its linter and gives a reason, and no file that the lax mode read as generated carries a finding. `nolintlint` does not report a directive for a linter the configuration does not enable, such as `gosec`, `nilerr`, or `revive`.
- Of the 377 findings, 288 in 76 packages are in test files (`_test.go` files and files under `tests/`), and 89 in 37 packages are in other files.
- Without `GOOS=linux`, on a darwin host, the run reports one type-checking error in `tests/testinfra/supervisedpod`, which builds only on Linux, and no other finding. The check therefore analyzes the Linux build on a host other than Linux. The counts below are from the Linux build, which is the gate's environment.
- The runs above took the host's architecture, `arm64`. By the owner's answer to GT-12 of 2026-11-20, both runs of the check, and its listing of the packages the binaries under `cmd/` import, pin `GOOS=linux`, `GOARCH=amd64`, `CGO_ENABLED=0`, and `GOWORK=off` on every host. The whole-tree command with that environment and the build tags above, over the branch `fix/gate-follow-ups` at `c7648cde3`, reports the same 377 findings with the same count in every package and linter, so the register did not change. No Go file in the tree is read only by an `arm64` build or only by a cgo build. The files behind `//go:build !linux` are outside both runs, as they were before.
- golangci-lint returns a cached finding under the file path of the checkout that first reported it. A run over `b5e4cab8f` with the cache that other worktrees on the same host had filled reported 354 findings, each under the path of another worktree; the same run with an empty cache reported 353 under this checkout's paths. The check therefore gives each checkout a cache directory of its own and fails on a finding whose path lies outside the checkout.

Findings per linter:

| Linter | Findings |
|:--|--:|
| `staticcheck` | 229 |
| `errcheck` | 111 |
| `unused` | 20 |
| `misspell` | 8 |
| `ineffassign` | 7 |
| `govet` | 2 |
| Total | 377 |

The `staticcheck` findings by check, counting each related-information entry that golangci-lint reports as a finding of its own: `SA1019` 67, `SA1012` 51, `QF1008` 30, `SA4006` 13, `SA4023` 10, `QF1001` 10, `ST1023` 9, `ST1005` 7, `S1016` 5, `QF1006` 4, `QF1011` 4, `SA4000` 4, `ST1011` 3, `S1024` 2, `SA5011` 2, `SA1008` 1, `SA9003` 1, `QF1003` 1, `QF1002` 1, `SA4010` 1, `S1011` 1, `S1000` 1, and `SA4017` 1.

Findings per package and linter:

| Package | `errcheck` | `govet` | `ineffassign` | `misspell` | `staticcheck` | `unused` | Total |
|:--|--:|--:|--:|--:|--:|--:|--:|
| `pkg/gateway/platformtools/mcp` | 0 | 0 | 0 | 0 | 48 | 1 | 49 |
| `pkg/gateway/api/admin` | 0 | 0 | 0 | 0 | 43 | 0 | 43 |
| `pkg/ops/events` | 36 | 0 | 0 | 0 | 1 | 1 | 38 |
| `pkg/workspaceplan` | 22 | 0 | 0 | 0 | 0 | 0 | 22 |
| `pkg/gateway/core/session/sessionserver` | 0 | 0 | 0 | 0 | 15 | 0 | 15 |
| `pkg/loadctl` | 0 | 0 | 0 | 0 | 9 | 0 | 9 |
| `tests/security` | 6 | 0 | 0 | 0 | 0 | 3 | 9 |
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
| `tests/integration` | 2 | 0 | 0 | 0 | 0 | 0 | 2 |
| `tests/load_local` | 2 | 0 | 0 | 0 | 0 | 0 | 2 |
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
| `tests/component/stores` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/conformance` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `tests/contract/rest_sessions` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/e2e_cloud` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/e2e_kind` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/load_cloud` | 0 | 0 | 0 | 1 | 0 | 0 | 1 |
| `tests/load_local/scenarios/auth_jwt_verify_throughput` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/load_local/scenarios/clock_skew_admission` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/load_local/scenarios/connection_exhaustion_recovery` | 0 | 0 | 0 | 0 | 0 | 1 | 1 |
| `tests/load_local/scenarios/controller_reconcile_rate` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/load_local/scenarios/streaming_reconnect_storm` | 0 | 0 | 1 | 0 | 0 | 0 | 1 |
| `tests/testinfra/commitfault` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/gateway` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `tests/testinfra/kind` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/loadgen` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| `tests/testinfra/opsprocess` | 1 | 0 | 0 | 0 | 0 | 0 | 1 |
| `tests/testinfra/sessiondriver` | 0 | 0 | 0 | 0 | 1 | 0 | 1 |
| Total | 111 | 2 | 7 | 8 | 229 | 20 | 377 |

## Gap

While these findings remain, the whole-tree golangci-lint run of the static tier fails on a count above the register or one the register lacks, and on a count that fell until the branch commits the lowered register, rather than on every finding. A branch that edits a line carrying one of these findings has to fix it, because the changed-line run reports it, and a branch that fixes one commits the lowered register.


## Suggested resolution

Fix the findings package by package, starting with the packages that carry the most, and recount with the command under "Evidence", with the build tags it names. The check lowers the register as the counts fall. When the count reaches zero, delete the register, make the whole-tree run in `cmd/lenny-test/lint_changed.go` fail the tier, remove the changed-line run, whose findings the whole-tree run then covers, update item 7 of the static-tier check list in `TESTING.md`, and close this issue.
