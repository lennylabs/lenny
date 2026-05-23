#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/down-loadgen.sh — tears down the tier-12
# load-runner pool. Idempotent. Does NOT touch the EKS cluster or
# the Lenny install; those are handled by down.sh.
#
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${AWS_REGION:-us-west-2}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadgen"
TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "down-loadgen.sh: ${TFVARS_FILE} not present; nothing to destroy" >&2
  exit 0
fi

cd "${TF_DIR}"
terraform init -input=false
terraform destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"
rm -f "${TFVARS_FILE}"

echo "down-loadgen.sh: pool destroyed"
