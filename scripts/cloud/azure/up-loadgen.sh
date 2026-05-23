#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/up-loadgen.sh — provisions the tier-12
# load-runner pool on Azure (VMSS + Service Bus + Managed Identity).
# Idempotent.
#
# Required environment:
#
#   AZURE_RESOURCE_GROUP                — resource group (must match up.sh).
#   AZURE_LOCATION                      — target region (default eastus).
#   LENNY_RELEASE                       — release name.
#   LENNY_LOADCTL_URL                   — base URL of the deployed loadctl
#                                         (output of up-loadctl.sh).
#   LENNY_LOADRUNNER_TOKEN              — bearer token the runner sends with
#                                         every loadctl callback.
#
# Optional:
#
#   LENNY_RUNNER_IMAGE_ID               — Shared Image Gallery image id.
#   LENNY_LOADRUNNER_REPORT_STORAGE_URL — azureblob://<account>/<container>.
#
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
LOCATION="${AZURE_LOCATION:-eastus}"
RG="${AZURE_RESOURCE_GROUP:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure/loadgen"

if [[ -z "${RG}" ]]; then echo "AZURE_RESOURCE_GROUP required" >&2; exit 2; fi
if [[ -z "${LENNY_LOADCTL_URL:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADCTL_URL is required (run up-loadctl.sh first)" >&2; exit 2
fi
if [[ -z "${LENNY_LOADRUNNER_TOKEN:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADRUNNER_TOKEN is required" >&2; exit 2
fi
for cli in az terraform; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "${cli} not on PATH" >&2; exit 3; }
done

AKS_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure"
cd "${AKS_TF_DIR}"
SUBNET_ID="$(terraform output -raw subnet_id 2>/dev/null || true)"
STORAGE_ACCOUNT="$(terraform output -raw storage_account_name 2>/dev/null || true)"
if [[ -z "${SUBNET_ID}" ]]; then
  echo "up-loadgen.sh: subnet_id output missing from ${AKS_TF_DIR}; run up.sh first" >&2; exit 4
fi

REPORTS_CONTAINER="${LENNY_RELEASE}-load-reports"
RUNNER_IMAGE_ID="${LENNY_RUNNER_IMAGE_ID:-/subscriptions/PLACEHOLDER/resourceGroups/${RG}/providers/Microsoft.Compute/galleries/lenny/images/loadrunner/versions/latest}"
REPORT_STORAGE_URL="${LENNY_LOADRUNNER_REPORT_STORAGE_URL:-azureblob://${STORAGE_ACCOUNT}/${REPORTS_CONTAINER}}"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE RG LOCATION SUBNET_ID RUNNER_IMAGE_ID REPORTS_CONTAINER \
       STORAGE_ACCOUNT LENNY_LOADCTL_URL LENNY_LOADRUNNER_TOKEN REPORT_STORAGE_URL
python3 - "${TFVARS_FILE}" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "resource_group_name": os.environ["RG"],
    "location": os.environ["LOCATION"],
    "subnet_id": os.environ["SUBNET_ID"],
    "runner_image_id": os.environ["RUNNER_IMAGE_ID"],
    "reports_container": os.environ["REPORTS_CONTAINER"],
    "storage_account_name": os.environ["STORAGE_ACCOUNT"],
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
