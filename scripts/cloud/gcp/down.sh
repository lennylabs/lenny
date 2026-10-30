#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/down.sh — tears down the per-release GCP
# resources up.sh provisioned.
#
# Runs `terraform destroy` against the tfvars up.sh wrote for the
# release. After a clean destroy the tfvars file is removed, so the
# next up.sh writes a fresh tfvars from the current env (new
# topology, new tiers).
#
# Required environment:
#
#   GCP_PROJECT
#
# Optional:
#
#   LENNY_RELEASE   — release prefix. Default lenny-e2e.

set -euo pipefail

RELEASE="${LENNY_RELEASE:-lenny-e2e}"
PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

if [[ ! -f "${TFVARS_FILE}" ]]; then
  echo "gcp/down.sh: ${TFVARS_FILE} not present; nothing to destroy" >&2
  exit 0
fi

if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "gcp/down.sh: neither terraform nor tofu on PATH" >&2
  exit 3
fi

cd "${TF_DIR}"
${TF} init -input=false
${TF} destroy -input=false -auto-approve -var-file="${TFVARS_FILE}"

rm -f "${TFVARS_FILE}"

echo "gcp/down.sh: release ${RELEASE} destroyed in project ${PROJECT}" >&2
