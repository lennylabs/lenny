#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/up-loadgen.sh — provisions the tier-12 load-runner
# pool on GCP (MIG + Pub/Sub + IAM). Idempotent.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${GCP_REGION:-us-central1}"
PROJECT_ID="${GCP_PROJECT:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp/loadgen"

if [[ -z "${PROJECT_ID}" ]]; then
  echo "up-loadgen.sh: GCP_PROJECT is required" >&2; exit 2
fi
for cli in gcloud terraform; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "up-loadgen.sh: ${cli} not on PATH" >&2; exit 3; }
done

GKE_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp"
cd "${GKE_TF_DIR}"
NETWORK="$(terraform output -raw network 2>/dev/null || true)"
SUBNETWORK="$(terraform output -raw subnetwork 2>/dev/null || true)"
if [[ -z "${NETWORK}" ]]; then
  echo "up-loadgen.sh: network output missing from ${GKE_TF_DIR}; run up.sh first" >&2; exit 4
fi

REPORTS_BUCKET="${LENNY_RELEASE}-load-reports"
RUNNER_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/lenny/lenny-loadrunner:latest"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
cat > "${TFVARS_FILE}" <<EOF
{
  "release": "${LENNY_RELEASE}",
  "project_id": "${PROJECT_ID}",
  "network": "${NETWORK}",
  "subnetwork": "${SUBNETWORK}",
  "region": "${REGION}",
  "runner_image": "${RUNNER_IMAGE}",
  "reports_bucket": "${REPORTS_BUCKET}"
}
EOF

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"
echo "up-loadgen.sh: pool ready"
terraform output
