# tier1_unit

Index for Tier 1 unit tests. Per TESTING.md §12.1 and §4, Lenny's unit tests live next to the code they cover under `pkg/.../foo_test.go`. This directory is a convention marker so the tier number has a home in the tree.

## What lives here

The directory holds Tier 1 unit tests that have no obvious source-tree home. Today that is just one suite:

| Path | Role |
|:--|:--|
| [`helm/helm_test.go`](helm/helm_test.go) | Helm chart-rendering unit tests. Shells out to `helm unittest charts/lenny`. Skips when `helm` or the `helm-unittest` plugin is missing. The chart-side cases live under `charts/lenny/tests/`. |

## What does not live here

The bulk of Tier 1 lives next to its package. For example:

- `pkg/audit/audit_test.go`
- `pkg/gateway/sessionserver/start_test.go`
- `pkg/tokenservice/grpc_test.go`

`lenny-test --tier unit` discovers both locations transparently.

## Build tag

These tests carry no build tag (Tier 1 is the default tier). The `helm/helm_test.go` file is in `package helm_test` and runs under `go test ./tests/tier1_unit/...`.
