#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/down-loadgen.sh — tears down the tier-12 GCP
# load-runner pool. Idempotent.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp/loadgen"
TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "down-loadgen.sh: nothing to destroy" >&2; exit 0
fi
cd "${TF_DIR}"
terraform init -input=false
terraform destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"
rm -f "${TFVARS_FILE}"
echo "down-loadgen.sh: pool destroyed"
