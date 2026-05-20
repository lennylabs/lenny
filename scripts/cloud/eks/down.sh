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

if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "eks/down.sh: neither terraform nor tofu on PATH" >&2
  exit 3
fi

cd "${TF_DIR}"
${TF} init -input=false
${TF} destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"

rm -f "${TFVARS_FILE}"

echo "eks/down.sh: cluster ${RELEASE}-eks destroyed in region ${REGION}" >&2
