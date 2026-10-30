#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/up-loadgen.sh — provisions the tier-12 load-runner
# pool on GCP (MIG + Pub/Sub + IAM). Idempotent.
#
# Required environment:
#
#   GCP_PROJECT                         — target project id.
#   GCP_REGION                          — target region (default us-central1).
#   LENNY_RELEASE                       — release name.
#   LENNY_LOADCTL_URL                   — base URL of the deployed loadctl
#                                         (output of up-loadctl.sh).
#   LENNY_LOADRUNNER_TOKEN              — bearer token the runner sends with
#                                         every loadctl callback.
#
# Optional:
#
#   LENNY_LOADRUNNER_REPORT_STORAGE_URL — object-storage URL the runner
#                                         uploads per-scenario k6 summaries
#                                         to. Default: gs://${reports_bucket}.
#
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
if [[ -z "${LENNY_LOADCTL_URL:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADCTL_URL is required (run up-loadctl.sh first)" >&2; exit 2
fi
if [[ -z "${LENNY_LOADRUNNER_TOKEN:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADRUNNER_TOKEN is required" >&2; exit 2
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
REPORT_STORAGE_URL="${LENNY_LOADRUNNER_REPORT_STORAGE_URL:-gs://${REPORTS_BUCKET}}"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE PROJECT_ID NETWORK SUBNETWORK REGION RUNNER_IMAGE \
       REPORTS_BUCKET LENNY_LOADCTL_URL LENNY_LOADRUNNER_TOKEN REPORT_STORAGE_URL
python3 - "${TFVARS_FILE}" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "project_id": os.environ["PROJECT_ID"],
    "network": os.environ["NETWORK"],
    "subnetwork": os.environ["SUBNETWORK"],
    "region": os.environ["REGION"],
    "runner_image": os.environ["RUNNER_IMAGE"],
    "reports_bucket": os.environ["REPORTS_BUCKET"],
    "loadctl_url": os.environ["LENNY_LOADCTL_URL"],
    "runner_token": os.environ["LENNY_LOADRUNNER_TOKEN"],
    "report_storage_url": os.environ["REPORT_STORAGE_URL"],
}
with open(sys.argv[1], "w") as f:
    json.dump(out, f, indent=2)
PY

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"
echo "up-loadgen.sh: pool ready"
terraform output
