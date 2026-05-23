# tier12_load_cloud

Cloud-cluster load runs for the §12.7 scenarios whose production sizing exceeds what a Kind smoke pool can serve. Companion to `tests/tier7b_load_kind/`.

## Why a separate directory

`tier7b_load_kind/` (PR cadence) drives a short, low-VU workload against the e2e Kind gateway and diffs against a stored baseline. The §12.7 production targets — 100 concurrent claims, 50-child fan-out, 500 streaming sessions — exceed the smoke pool's capacity. The cloud-load directory re-runs those scenarios against a real cloud cluster whose warm pools are sized for the spec.

The cloud-load suite is opt-in and runs from `scripts/cloud/<provider>/run-load.sh`.

## Layout

| Path | Role |
|:--|:--|
| `scaffolds_test.go` | Per-scenario cloud-load tests. Each test gates on `LENNY_LOAD_CLOUD_PROVIDERS`, the per-mode warm-pool readiness, and `LENNY_LOAD_SCALE`. |
| `scenarios/` | Cloud-only k6 scenarios. |
| `<mode>_test.go` | Per-execution-mode drivers (`session_mode_test.go`, `task_mode_test.go`, `concurrent_workspace_test.go`, `concurrent_stateless_test.go`). |

## Environment

The suite is gated on `LENNY_LOAD_CLOUD_PROVIDERS` (the same gating pattern as Tier 6's `LENNY_CLOUD_PROVIDERS`). Without that env the suite is a no-op.

`LENNY_LOAD_SCALE` selects one of three sizing profiles:

| Scale | Targets | Cluster sizing |
|:--|:--|:--|
| `small` (default) | 10–50 concurrent sessions, 5–10 child fan-out, 25–50 streaming sessions. ~5–10 min per scenario. | cloud-small (2–4 t3.medium nodes on AWS, equivalent elsewhere) |
| `medium` | 50–200 concurrent sessions, 10–25 child fan-out, 100–250 streaming sessions. | cloud-medium |
| `large` | 100+ concurrent claims, 50-child fan-out, 500 streaming sessions. The full §12.7 production envelope. | cloud-large |

Per-provider Terraform overlays under `deploy/terraform/cloud/<provider>/` size the cluster for the chosen scale.

## Build tag and invocation

```bash
LENNY_LOAD_CLOUD_PROVIDERS=aws LENNY_LOAD_SCALE=small \
  scripts/cloud/aws/run-load.sh

# Or invoke the harness directly once the cluster is up:
LENNY_LOAD_CLOUD_PROVIDERS=aws lenny-test --tier load_cloud
```

Each file declares `//go:build load_cloud`. The k6 binary, the provider CLIs (`aws`, `gcloud`, `az`), and a configured kubeconfig must be on `$PATH`; any missing dependency is an external-dependency skip.

## Cross-references

- TESTING.md §12.7 — load and SLO scenarios
- TESTING.md §10 (Profile: `cloud`) — cloud provisioning
- `tests/tier7b_load_kind/` — the PR-cadence smoke companion
- `scripts/cloud/<provider>/run-load.sh` — provisioning and invocation wrappers
