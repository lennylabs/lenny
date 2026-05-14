# tests/testinfra

Shared helper packages for the Lenny test infrastructure. Every Tier 1+ test imports from here when it needs a backing service, a deterministic clock, a fixture, a goleak snapshot, or any other piece of plumbing the harness provides.

The full specification lives in TESTING.md. This README is the directory map.

## Package index

| Path | Role |
|:--|:--|
| [`assertions/`](assertions/) | Typed comparison helpers; JSON equality with ordering hints; structural matchers for state machines. The project's stand-in for testify/gomega. |
| [`chaos/`](chaos/) | Fault-injection driver for Tier 8 (chaos-mesh adapter, network partition, pod kill, clock skew). |
| [`cloud/`](cloud/) | Tier 6 cloud-provider authentication and cluster acquisition (GKE, EKS, AKS). |
| [`compose/`](compose/) | `docker compose` lifecycle wrapper for Tier 4 integration tests (gateway + stores + mocks on a single host). |
| [`containers/`](containers/) | Per-test Postgres, Redis, and MinIO container managers. Each test gets a fresh schema or keyspace. |
| [`envtest/`](envtest/) | controller-runtime envtest harness for Tier 2 K8s API-server interactions. |
| [`fixtures/`](fixtures/) | Seed-data loaders for the canonical tenants, layers, runtimes, and OAuth providers used across tiers. |
| [`gateway/`](gateway/) | Boots `cmd/lenny-gateway` as a subprocess on a random port; returns the base URL. |
| [`golden/`](golden/) | Golden-file roundtrip helpers with `-update` flag for accepting diffs. |
| [`goleak/`](goleak/) | Wrapper around `go.uber.org/goleak` keyed on the test name; the canonical place to install per-test leak detection. |
| [`kind/`](kind/) | Kind cluster lifecycle for Tier 5 (cluster create, kubeconfig, kubectl apply, teardown). |
| [`load/`](load/) | k6 scenario runner for Tier 7; reports back to the Phase 14.5 baseline comparator. |
| [`mocks/`](mocks/) | Mock external services: LLM providers, OIDC issuer, OAuth connector, KMS, SIEM endpoint, OTel collector. |
| [`ports/`](ports/) | Random-free-port allocator for subprocess-style tests. |
| [`randctl/`](randctl/) | Deterministic RNG keyed by test name; substitutes for `crypto/rand` in tests. |
| [`runtimes/`](runtimes/) | Test runtime images and conformance fixtures (intentionally-malformed runtimes the §11 conformance harness asserts against). |
| [`schematest/`](schematest/) | JSON Schema and protobuf round-trip helpers. |
| [`sdkhelper/`](sdkhelper/) | Per-language SDK conformance scaffolding; invoked from Tier 10. |
| [`security/`](security/) | SBOM, cosign verification, ZAP scanner driver, trivy, kube-bench wrappers. |
| [`stubs/`](stubs/) | In-process fault models that the gateway must handle gracefully (KMS outages, OAuth provider 5xx, etc.). |
| [`timectl/`](timectl/) | `Clock` interface; tests pass a frozen or advanceable clock where production uses wall time. |
| [`wait/`](wait/) | Polling helpers (`Until`, `WithBackoff`) used by every helper that needs to converge on a ready condition. |

## Importing a helper

Helpers are public Go packages under `github.com/lennylabs/lenny/tests/testinfra/<name>`. A typical Tier 2 component test:

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
2. The package's exported surface must accept `testing.TB` as the first argument of any constructor; register cleanup via `t.Cleanup`.
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
