# tier7_load

PR-cadence load and SLO smoke runs against the Kind e2e gateway, plus the canonical baseline corpus.

Tier 7 is split into two directories:

- `tier7_load/` (this directory) — Kind-based smoke runs that drive a short, low-VU workload against the e2e cluster and diff the result against a stored baseline. Runs on every PR.
- `tier7_load_cloud/` — Opt-in cloud-profile runs that exercise the §12.7 production sizing targets (100+ concurrent claims, 50-child fan-out, 500+ streaming sessions). Drives a real cloud cluster via `scripts/cloud/<provider>/run-load.sh`.

The two directories share scenario definitions but bind to different harnesses and run cadences.

## Layout

| Path | Role |
|:--|:--|
| `scaffolds_test.go` | Per-scenario smoke runs. Each test gates on `kind.InstallLenny` and `load.SkipUnlessAvailable`, port-forwards the gateway, runs the k6 scenario, and diffs the result against `baselines/<scenario>.json`. |
| `scenarios/<name>/main.js` | The k6 script for scenario `<name>`. Currently: `audit_lock`, `checkpoint_duration`, `credential_lifecycle`, `credential_rotation_under_load`, `delegation_fanout`, `delegation_fanout_mcp`, `experiment_load`, `pod_claim_latency`, `post_hardening_slo`, `postgres_write_burst`, `session_throughput`, `startup_latency`, `streaming_reconnect`, `streaming_throughput`. |
| `baselines/<scenario>.json` | Per-percentile baseline for `<scenario>`. The smoke run diffs against this corpus. `LENNY_UPDATE_BASELINE=1` reseeds the file. |
| `<subject>_test.go` | Subject-specific drivers when the scenario needs setup beyond the scaffolds (e.g., `streaming_test.go`, `delegation_test.go`). |

## Profile

Smoke runs use ~20 VUs over ~25 seconds. That produces thousands of samples per scenario for a stable baseline without taxing the Kind cluster's modest pod-creation rate. Scenarios that target the production SLOs (high concurrent claims, large fan-outs, hundreds of sustained streaming sessions) are phase-gated out of the smoke and run in `tier7_load_cloud/`.

## Build tag and invocation

```bash
lenny-test --tier load
lenny-test --tier load --subset baselines    # just the baseline-diffing subset
```

Each file declares `//go:build load`. The k6 binary must be on `$PATH`; missing k6 is an external-dependency skip.

## Cross-references

- TESTING.md §12.7 — load and SLO scenarios
- TESTING.md §22.5 — baseline regression budget
- `tests/tier7_load_cloud/` — the cloud-scale companion
