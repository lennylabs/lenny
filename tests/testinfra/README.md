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
| [`bindattempt/`](bindattempt/) | Adapter fixture and case battery for the slot bind attempt admission and `Shutdown` rules. The Tier 3 contract suite drives it over gRPC and the Tier 10 conformance battery drives it in process. |
| [`chaos/`](chaos/) | Fault-injection driver for Tier 8 (chaos-mesh adapter, network partition, pod kill, clock skew, toxiproxy bridge). |
| [`cloud/`](cloud/) | Tier 6 cloud-provider authentication and cluster acquisition for GKE, EKS, and AKS. |
| [`compose/`](compose/) | `docker compose` lifecycle wrapper for Tier 4 integration tests (gateway, stores, and mocks on a single host). |
| [`containers/`](containers/) | Per-test Postgres, Redis, and MinIO container managers. Each test gets a fresh schema or keyspace. `StartMinIO` runs `pgsty/silo`, the maintained fork of the MinIO server, pinned by tag and index digest; `LENNY_MINIO_IMAGE` replaces it. |
| [`embpg/`](embpg/) | In-process embedded PostgreSQL 16 binary bundle for store-package tests (`fergusstrange/embedded-postgres` wrapper). Used where a test needs a real Postgres without a container runtime. |
| [`envtest/`](envtest/) | controller-runtime envtest harness for Tier 2 K8s API-server interactions. |
| [`fixtures/`](fixtures/) | Seed-data loaders for the canonical tenants, layers, runtimes, and OAuth providers used across tiers. Subpackages: `generators/`, `seed/`. |
| [`fuzz/`](fuzz/) | Fuzz-harness scaffolding plus the `crashes/` corpus mirror (TESTING.md §19.2). |
| [`gateway/`](gateway/) | Boots `cmd/lenny-gateway` as a subprocess on a random port and returns the base URL. |
| [`golden/`](golden/) | Golden-file roundtrip helpers with `-update` flag for accepting diffs. |
| [`goleak/`](goleak/) | Wrapper around `go.uber.org/goleak` keyed on the test name; the canonical place to install per-test leak detection. |
| [`heartbeatsilence/`](heartbeatsilence/) | Runtime fixture for a runtime that hangs between turns. It runs an unmodified reference runtime as a child and stops forwarding heartbeats after a directive message. Its `Dockerfile` layers it on a reference runtime image for the Tier 5 heartbeat-silence pools that `kind/install.sh` installs. |
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
| [`runtimes/`](runtimes/) | Conformance-fixture runtime images. `runtimes/conformance-fixtures/` carries intentionally-malformed runtimes (`blocked-stdin/`, `late-shutdown/`, `malformed-jsonl/`, `missing-heartbeat-ack/`, `oversize-payload/`, `unknown-message-type/`) the TESTING.md §12.10 conformance harness asserts against. Build with `go build -o ./bin/ ./tests/testinfra/runtimes/conformance-fixtures/...`. |
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

## Shared Kind cluster database

Parallel worktrees install onto the same Kind cluster, named by `LENNY_KIND_CLUSTER` with the default `lenny-e2e`, and step 8 of `kind/install.sh` migrates the one Postgres in `lenny-system` to the installing branch's migrations. A branch with a newer migration leaves the database ahead of every branch that lacks it, and a migrate that stops part-way leaves the `schema_migrations` row dirty.

Before it migrates, `kind/install.sh` reads `schema_migrations` in a read-only transaction and compares the version with the highest `migrations/NNNN_*.up.sql` in the tree. It proceeds when the table is absent or empty, or when the version is at or below the branch's highest migration. It stops before migrating when the row is dirty or when the version is ahead of the branch, and names the version, the branch's highest migration, and the procedure below. `LENNY_SKIP_MIGRATION_GUARD=1` runs the migration regardless, for deliberate use only: `lenny-migrate up` still fails on a dirty or ahead database.

golang-migrate records only the version of an applied migration, so a database another branch migrated to its own file at a shared number reads as equal to this branch. After each successful migrate, `kind/install.sh` records the sha256 and file name of every applied up migration in the ConfigMap `lenny-e2e-migrations` in `lenny-system`, together with the OID of the database the record describes. Before the next migrate, the guard compares each recorded entry at or below the database version with the branch's file of the same number, and stops when a name or a sha256 differs or when the branch lacks the recorded number. It names each colliding version, the recorded file, and the branch's file. The remedies are to renumber the branch's migration above the integration branch's highest, or to step the database back below the colliding number with the `lenny-migrate` image of the worktree that installed it, as the step-back procedure below describes. The record is a ConfigMap rather than a table, so neither the guard nor the recorder writes to the shared database.

The content check has limits. A database with no record, or one whose OID differs from the record's because the database was dropped and recreated, is not checked, and the guard logs that. An install from a branch that predates the record migrates without updating it, so the versions it applied stay unrecorded until a later install records them. An install that `LENNY_SKIP_MIGRATION_GUARD=1` let through deletes the record, because its content is unverified. The tier-0 test `TestMigrationNumbersAreUnique` fails a tree whose `migrations/` holds two migrations at one number, which catches a collision once both branches merge.

The database is shared state. Do not edit `schema_migrations` by hand, and run either procedure only with the owner's approval, because both change the database every other worktree tests against. Read the current row first:

```
kubectl --context kind-lenny-e2e -n lenny-system exec deploy/lenny-postgres -- \
  sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT version, dirty FROM schema_migrations"'
```

### Step-back procedure for a database ahead of the branch

The first remedy is to merge the integration branch into the installing branch, so the branch carries the newer migration, and re-run `kind/install.sh`. When the branch has to keep its migration set, step the database back to the branch's highest migration `<n>`:

1. Choose a `lenny-migrate` image built from a tree that contains the down migration of every version above `<n>`. The image of the worktree that installed the newer migration qualifies. List the loaded tags with `docker exec lenny-e2e-control-plane crictl images | grep lenny-migrate`.
2. Copy `kind/migrate-job.yaml` to a scratch file. Rename the Job, for example to `lenny-e2e-migrate-goto-<n>`, set `backoffLimit: 0`, set the image to the tag from step 1, and replace the `up` argument with `--break-glass`, `--confirm`, `goto`, and `"<n>"`.
3. Apply the scratch file with `kubectl --context kind-lenny-e2e apply -f <file>`, and wait for it with `kubectl --context kind-lenny-e2e -n lenny-system wait --for=condition=Complete job/lenny-e2e-migrate-goto-<n> --timeout=180s`.
4. Read the row again and confirm it is `<n>|f`.
5. Re-run `kind/install.sh` from the branch.

The Job runs each down migration from the current version to `<n>`, so it drops the schema objects the newer migrations added. The worktree that installed them re-applies them on its next install.

### Reset procedure for a dirty database

`lenny-migrate goto` and `lenny-migrate up` both refuse a dirty database, and clearing the flag by hand leaves a partly applied migration recorded as complete. Reset the database instead:

1. Stop every test run against the cluster, in every worktree.
2. Drop and recreate the `lenny` database, connecting to the `postgres` maintenance database:

   ```
   kubectl --context kind-lenny-e2e -n lenny-system exec deploy/lenny-postgres -- \
     sh -c 'psql -U "$POSTGRES_USER" -d postgres -c "DROP DATABASE lenny WITH (FORCE)" -c "CREATE DATABASE lenny OWNER lenny"'
   ```

3. Re-run `kind/install.sh` from the integration branch, so the database ends at the integration branch's highest migration.
4. Read the row again and confirm it is not dirty.

The reset discards every row the tests had written. The install re-creates the data its own steps seed.

## TESTING.md cross-references

| Subsystem | TESTING.md section |
|:--|:--|
| Container provisioning | §10 |
| Spec traceability | §5 |
| Verdict format | §7 |
| Test authoring conventions | §17 |
| Fixtures and golden data | §18 |
| CI pipeline | §20 |
