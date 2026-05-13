# GitHub Actions workflows

Lenny's CI is documented in [`../../TESTING.md`](../../TESTING.md) §20. This directory ships the Phase 0 workflow skeletons. Each workflow is a thin wrapper around `lenny-test`; the harness, not the YAML, is the source of truth.

| Workflow | Trigger | Purpose |
|:---------|:--------|:--------|
| `pr.yml` | `pull_request`, `push` to feature branches | PR gate. Tiers 0–4 plus critical-path higher tiers. Target < 15 minutes. |
| `nightly.yml` | `schedule` daily 05:00 UTC, `workflow_dispatch` | Full e2e Kind, rotated cloud subset, security, path-specific load. Target < 2 hours. |
| `weekly.yml` | `schedule` Sundays 06:00 UTC, `workflow_dispatch` | All three providers in `cloud-small` shape. Full-system load. Target < 4 hours. |
| `pre-release.yml` | `workflow_dispatch` (tag input) | All three providers × both shapes. Full chaos. Pen-test driver. SLO baseline diff. Target < 8 hours. |
| `phase-gate.yml` | `workflow_dispatch` (phase input) | Per-phase completion gate; artifact required for phase-implementation merges. |
| `cache-prune.yml` | `schedule` Sundays 04:00 UTC | Removes caches older than 7 days to stay under the 10 GB ceiling. |
| `reusable/lenny-test.yml` | `workflow_call` | Run `lenny-test` with a named selector and upload the verdict. |
| `reusable/cloud-auth.yml` | `workflow_call` | OIDC federation for GKE / EKS / AKS. |

## Phase 0 state

Most jobs are placeholders that echo the phase in which the tier ships. The PR pipeline's `pr-fast`, `static`, `unit`, and `docs` jobs are functional and run on every push. The rest light up incrementally per TESTING.md §13.

## Self-hosted runners

Tier 5+ jobs migrate to self-hosted runners (managed by `actions-runner-controller` in `lenny-ci-cluster`) as those tiers come online. The current placeholders target `ubuntu-latest`; the runner label is changed in the phase that first implements each tier. See TESTING.md §20.3.

## OIDC and secrets

CI authenticates to cloud providers via OIDC. No long-lived service-account keys are stored in repository secrets. Trust configuration is provisioned via Terraform under `deploy/terraform/cloud/`. See TESTING_DEPENDENCIES.md §13.

## Adding a new workflow

1. Document the trigger and the target wall-clock in TESTING.md §20.
2. Add the workflow file here.
3. Add a row to the table above.
4. If it consumes a new group, ensure `tests/groups.yaml` defines the group.
