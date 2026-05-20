#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/eks/down.sh — tears down the tier-6 EKS cluster.
#
# Runs `terraform destroy` against the same per-release tfvars
# scripts/cloud/eks/up.sh produced. Safe to re-run; the tfvars file is
# deleted only after a clean destroy.

set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
RELEASE="${LENNY_RELEASE:-lenny-e2e}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "eks/down.sh: ${TFVARS_FILE} not present; nothing to destroy" >&2
  exit 0
fi

cd "${TF_DIR}"
terraform init -input=false
terraform destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"

rm -f "${TFVARS_FILE}"

echo "eks/down.sh: cluster ${RELEASE}-eks destroyed in region ${REGION}" >&2
