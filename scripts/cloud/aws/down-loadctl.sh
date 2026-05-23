#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/down-loadctl.sh — tears down the tier-12 AWS
# control plane. Idempotent.
# TESTING.md §12.12 / Wave 6.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadctl"
TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "down-loadctl.sh: nothing to destroy" >&2; exit 0
fi
cd "${TF_DIR}"
terraform init -input=false
terraform destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"
rm -f "${TFVARS_FILE}"
echo "down-loadctl.sh: control plane destroyed"
