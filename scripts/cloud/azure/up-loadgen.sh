#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/up-loadgen.sh — provisions the tier-12
# load-runner pool on Azure (VMSS + Service Bus + Managed Identity).
# Idempotent.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
LOCATION="${AZURE_LOCATION:-eastus}"
RG="${AZURE_RESOURCE_GROUP:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure/loadgen"

if [[ -z "${RG}" ]]; then echo "AZURE_RESOURCE_GROUP required" >&2; exit 2; fi
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

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
cat > "${TFVARS_FILE}" <<EOF
{
  "release": "${LENNY_RELEASE}",
  "resource_group_name": "${RG}",
  "location": "${LOCATION}",
  "subnet_id": "${SUBNET_ID}",
  "runner_image_id": "${RUNNER_IMAGE_ID}",
  "reports_container": "${REPORTS_CONTAINER}",
  "storage_account_name": "${STORAGE_ACCOUNT}"
}
EOF

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"
echo "up-loadgen.sh: pool ready"
terraform output
