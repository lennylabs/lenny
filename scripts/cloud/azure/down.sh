#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/down.sh — tears down the per-release Azure
# resources up.sh provisioned.
#
# Runs `terraform destroy` against the tfvars up.sh wrote for the
# release. After a clean destroy the tfvars file is removed, so the
# next up.sh writes a fresh tfvars from the current env (new
# topology, new SKUs).
#
# Required environment:
#
#   AZURE_SUBSCRIPTION_ID
#   AZURE_RESOURCE_GROUP
#
# Optional:
#
#   LENNY_RELEASE   — release prefix. Default lenny-e2e.

set -euo pipefail

RELEASE="${LENNY_RELEASE:-lenny-e2e}"
SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID:?AZURE_SUBSCRIPTION_ID must be set}"
RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "azure/down.sh: ${TFVARS_FILE} not present; nothing to destroy" >&2
  exit 0
fi

if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "azure/down.sh: neither terraform nor tofu on PATH" >&2
  exit 3
fi

cd "${TF_DIR}"
${TF} init -input=false
${TF} destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"

rm -f "${TFVARS_FILE}"

echo "azure/down.sh: release ${RELEASE} destroyed in resource group ${RESOURCE_GROUP}" >&2
