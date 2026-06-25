# tests/testinfra

Shared helper packages and asset directories for Lenny's test infrastructure. Every Tier 1+ test imports from here when it needs a backing service, a deterministic clock, a fixture, a goleak snapshot, or any other piece of plumbing the harness provides.

The full specification lives in `TESTING.md`. This README is the directory map.

## Go packages

Each entry is a package under `github.com/lennylabs/lenny/tests/testinfra/<name>`.

| Path | Role |
|:--|:--|
| [`admission/`](admission/) | Validating-admission webhook test fixture. |
| [`assertions/`](assertions/) | Typed comparison helpers; JSON equality with ordering hints; structural matchers for state machines. The project's stand-in for testify and gomega. |
| [`audit/`](audit/) | Audit-pipeline helpers (chain inspection, OCSF envelope assertions, SIEM consumer harness). |
| [`chaos/`](chaos/) | Fault-injection driver for Tier 8 (chaos-mesh adapter, network partition, pod kill, clock skew, toxiproxy bridge). |
| [`cloud/`](cloud/) | Tier 6 cloud-provider authentication and cluster acquisition for GKE, EKS, and AKS. |
| [`compose/`](compose/) | `docker compose` lifecycle wrapper for Tier 4 integration tests (gateway, stores, and mocks on a single host). |
| [`containers/`](containers/) | Per-test Postgres, Redis, and MinIO container managers. Each test gets a fresh schema or keyspace. |
| [`embpg/`](embpg/) | In-process embedded PostgreSQL 16 binary bundle for store-package tests (`fergusstrange/embedded-postgres` wrapper). Used where a test needs a real Postgres without a container runtime. |
| [`envtest/`](envtest/) | controller-runtime envtest harness for Tier 2 K8s API-server interactions. |
| [`fixtures/`](fixtures/) | Seed-data loaders for the canonical tenants, layers, runtimes, and OAuth providers used across tiers. Subpackages: `generators/`, `seed/`. |
| [`fuzz/`](fuzz/) | Fuzz-harness scaffolding plus the `crashes/` corpus mirror (§19.2). |
| [`gateway/`](gateway/) | Boots `cmd/lenny-gateway` as a subprocess on a random port and returns the base URL. |
| [`golden/`](golden/) | Golden-file roundtrip helpers with `-update` flag for accepting diffs. |
| [`goleak/`](goleak/) | Wrapper around `go.uber.org/goleak` keyed on the test name; the canonical place to install per-test leak detection. |
| [`helm/`](helm/) | Helm chart rendering and apply helpers. |
| [`kind/`](kind/) | Kind cluster lifecycle for Tier 5 (cluster create, kubeconfig, kubectl apply, port-forward, teardown). |
| [`load/`](load/) | k6 scenario runner and baseline diff utilities for Tier 7. |
| [`matrix/`](matrix/) | Contract-test matrix combinatorics. |
| [`mocks/`](mocks/) | Mock external services: LLM providers, OAuth connector, OTel collector. Subpackages mirror the mocked service names. |
| [`ports/`](ports/) | Random free-port allocator for subprocess-style tests. |
| [`randctl/`](randctl/) | Deterministic RNG keyed by test name; substitutes for `crypto/rand` in tests. |
| [`schematest/`](schematest/) | JSON Schema and protobuf round-trip helpers. |
| [`security/`](security/) | SBOM, cosign verification, ZAP scanner driver, trivy, and kube-bench wrappers. Subpackages: `cosign/`, `kubebench/`, `pentest/`, `sbom/`, `zap/`. |
| [`sessiondriver/`](sessiondriver/) | Live-session test driver (HTTP, SSE, chaos and security bridges) used by Tiers 5+. |
| [`stubs/`](stubs/) | In-process fault models the gateway must handle gracefully. Subpackages: `kms/`, `llmprovider/`, `oidc/`, `siem/`. |
| [`timectl/`](timectl/) | `Clock` interface; tests pass a frozen or advanceable clock where production uses wall time. |
| [`tokenservice/`](tokenservice/) | Token-service minting helpers for mTLS-bearing client setups. |
| [`wait/`](wait/) | Polling helpers (`Until`, `WithBackoff`) used by every helper that needs to converge on a ready condition. |

## Asset directories (not Go packages)

A few subdirectories under `testinfra/` carry only YAML, fixtures, or a single subcommand. They are listed separately so a reader does not expect a Go package at the top of each path.

| Path | Contents |
|:--|:--|
| [`k8s/`](k8s/) | Kubernetes manifests applied by the Kind install and cloud-load drivers: `datastores.yaml` (Postgres, Redis, MinIO fixtures) and `agent-workload-load.yaml.tmpl` (per-mode load runtime template). |
| [`runtimes/`](runtimes/) | Conformance-fixture runtime images. `runtimes/conformance-fixtures/` carries intentionally-malformed runtimes (`blocked-stdin/`, `late-shutdown/`, `malformed-jsonl/`, `missing-heartbeat-ack/`, `oversize-payload/`, `unknown-message-type/`) the §12.10 conformance harness asserts against. Build with `go build -o ./bin/ ./tests/testinfra/runtimes/conformance-fixtures/...`. |
| [`sdkhelper/`](sdkhelper/) | SDK conformance scaffolding. `sdkhelper/echo/` is a reference binary that implements the SDK contract helper protocol; Tier 3 SDK harness tests build and drive it. Real SDK helpers (`sdks/client/python/test-helper`, etc.) follow the same protocol over HTTP. |

## Importing a helper

Go helpers live under `github.com/lennylabs/lenny/tests/testinfra/<name>`. A typical Tier 2 component test:

```go
import (
    "testing"

    "github.com/lennylabs/lenny/tests/testinfra/containers"
    "github.com/lennylabs/lenny/tests/testinfra/timectl"
)

func TestSomething(t *testing.T) {
    pg := containers.NewPostgres(t)
    clock := timectl.NewFake(t, "2026-01-01T00:00:00Z")
    // ...
}
```

Every helper accepts `testing.TB` and registers its own `t.Cleanup` so the test author does not call teardown explicitly.

## Adding a helper

1. Create the package under `tests/testinfra/<name>/` with a single `<name>.go` and an optional `<name>_test.go`.
2. The package's exported surface must accept `testing.TB` as the first argument of any constructor and register cleanup via `t.Cleanup`.
3. Add a row to the index above.
4. Cross-reference the relevant TESTING.md section (for example, container helpers fall under §10; timectl falls under §17.4).

## Determinism rules

Helpers must not call `time.Now`, `crypto/rand`, `os/exec` against host binaries, or read environment variables outside of explicit configuration entry points. The `lint-determinism` static check in Tier 0 catches the common violations; the helpers must stay clean even when the linter is silent.

## Skip behavior

A helper that depends on an external dependency (`docker`, `kind`, `setup-envtest`, a cloud kubeconfig) calls `t.Skip` with a reason that names the missing dependency. The harness preserves the skip reason in `tests/results/latest.json` so the verdict reflects which environments ran which tiers.

## TESTING.md cross-references

| Subsystem | TESTING.md section |
|:--|:--|
| Container provisioning | §10 |
| Spec traceability | §5 |
| Verdict format | §7 |
| Test authoring conventions | §17 |
| Fixtures and golden data | §18 |
| CI pipeline | §20 |
