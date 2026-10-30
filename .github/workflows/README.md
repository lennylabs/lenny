# GitHub Actions workflows

Lenny's CI is documented in [`../../TESTING.md`](../../TESTING.md) §20. This directory ships the Phase 0 workflow skeletons. Each workflow is a thin wrapper around `lenny-test`; the harness, not the YAML, is the source of truth.

| Workflow | Trigger | Purpose |
|:---------|:--------|:--------|
| `pr.yml` | `pull_request`, `push` to feature branches | PR gate. Tiers 0–4 plus critical-path higher tiers. Target < 15 minutes. |
| `dco.yml` | `pull_request` | Verifies every PR commit carries a DCO `Signed-off-by` trailer. |
| `secret-scan.yml` | `pull_request`, `push` to feature branches | Runs gitleaks over the introduced commits; fails on a positive hit. |
| `nightly.yml` | `schedule` daily 05:00 UTC, `workflow_dispatch` | Full e2e Kind, rotated cloud subset, security, path-specific load. Target < 2 hours. |
| `weekly.yml` | `schedule` Sundays 06:00 UTC, `workflow_dispatch` | All three providers in `cloud-small` shape. Full-system load. Target < 4 hours. |
| `pre-release.yml` | `workflow_dispatch` (tag input) | All three providers × both shapes. Full chaos. Pen-test driver. SLO baseline diff. Target < 8 hours. |
| `release.yml` | `push` of a `v*` tag | Release-time supply chain: multi-arch image build, cosign signing, CycloneDX SBOM attestation, signed Helm chart, GitHub release, krew-index PR. |
| `sdk-publish.yml` | `push` of a `v*` tag | Publishes the runtime-author SDKs: `runtime-sdk-go` (Go proxy), `lenny-runtime` (PyPI Trusted Publishing), `@lennylabs/runtime-sdk` (npm provenance). |
| `phase-gate.yml` | `workflow_dispatch` (phase input) | Per-phase completion gate; artifact required for phase-implementation merges. |
| `cache-prune.yml` | `schedule` Sundays 04:00 UTC | Removes caches older than 7 days to stay under the 10 GB ceiling. |
| `reusable/lenny-test.yml` | `workflow_call` | Run `lenny-test` with a named selector and upload the verdict. |
| `reusable/cloud-auth.yml` | `workflow_call` | OIDC federation for GKE / EKS / AKS. |

## Phase 0 state

The PR pipeline runs tiers 0–4 (`static`, `unit`, `component`, `contract`, `integration`) plus `docs` on every push and pull request, alongside `pr-fast`, `dco`, and `secret-scan`. The `e2e-kind-critical` and `conformance-bundled` jobs run their critical-path subsets; for a community fork PR the Kind tier is held until a maintainer applies the `ok-to-test` label. Higher cluster, cloud, and load tiers light up incrementally per TESTING.md §13.

## Self-hosted runners

Tier 5+ jobs migrate to self-hosted runners (managed by `actions-runner-controller` in `lenny-ci-cluster`) as those tiers come online. The current placeholders target `ubuntu-latest`; the runner label is changed in the phase that first implements each tier. See TESTING.md §20.3.

## OIDC and secrets

CI authenticates to cloud providers via OIDC. No long-lived service-account keys are stored in repository secrets. Trust configuration is provisioned via Terraform under `deploy/terraform/cloud/`. See TESTING_DEPENDENCIES.md §13.

The release pipeline (`release.yml`, `sdk-publish.yml`) signs container images keyless with cosign and publishes the `lenny-runtime` PyPI package via Trusted Publishing, both of which use the workflow OIDC token and need no stored secret. The remaining release secrets an operator must configure are:

| Secret | Used by | Purpose |
|:--|:--|:--|
| `HELM_GPG_PRIVATE_KEY` | `release.yml` | ASCII-armored PGP private key for `helm package --sign` chart provenance. |
| `HELM_GPG_PASSPHRASE` | `release.yml` | Passphrase that unlocks `HELM_GPG_PRIVATE_KEY`. |
| `HELM_GPG_KEY_NAME` | `release.yml` | UID/key name passed to `helm package --key`. |
| `KREW_INDEX_TOKEN` | `release.yml` | PAT with push access to the krew-index fork and pull-request scope. |
| `GO_PROXY_PUSH_TOKEN` | `sdk-publish.yml` | PAT that pushes tags to the `runtime-sdk-go` mirror repository. |
| `NPM_TOKEN` | `sdk-publish.yml` | npm automation token for the `@lennylabs` org with publish rights. |

PyPI Trusted Publishing for `lenny-runtime` must be registered on pypi.org with this repository and the `sdk-publish.yml` workflow as the trusted publisher.

## Adding a new workflow

1. Document the trigger and the target wall-clock in TESTING.md §20.
2. Add the workflow file here.
3. Add a row to the table above.
4. If it consumes a new group, ensure `tests/groups.yaml` defines the group.
