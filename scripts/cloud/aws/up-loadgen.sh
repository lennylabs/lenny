#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/up-loadgen.sh — provisions the tier-12
# load-runner pool (ASG + SQS + IAM + metrics collector) against
# an EKS cluster already created by up.sh.
#
# Idempotent. Re-running re-applies the loadgen terraform module
# against the existing state; no resources are destroyed.
#
# Required environment:
#
#   AWS_PROFILE        — SSO profile.
#   AWS_REGION         — target region (default us-west-2).
#   LENNY_RELEASE      — release name (must match the EKS up.sh release).
#
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${AWS_REGION:-us-west-2}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadgen"

for cli in aws terraform; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "up-loadgen.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done

if ! aws sts get-caller-identity >/dev/null 2>&1; then
  echo "up-loadgen.sh: aws sts get-caller-identity failed" >&2
  exit 3
fi

# Read VPC + subnet IDs from the EKS up.sh's terraform outputs.
EKS_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
cd "${EKS_TF_DIR}"
VPC_ID="$(terraform output -raw vpc_id 2>/dev/null || true)"
PRIVATE_SUBNETS="$(terraform output -json private_subnet_ids 2>/dev/null || echo '[]')"
if [[ -z "${VPC_ID}" ]]; then
  echo "up-loadgen.sh: could not read vpc_id from ${EKS_TF_DIR}/terraform.tfstate; run up.sh first" >&2
  exit 4
fi

REPORTS_BUCKET="${LENNY_RELEASE}-load-reports"
RUNNER_IMAGE="$(aws sts get-caller-identity --query Account --output text).dkr.ecr.${REGION}.amazonaws.com/lenny-loadrunner:latest"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
cat > "${TFVARS_FILE}" <<EOF
{
  "release": "${LENNY_RELEASE}",
  "vpc_id": "${VPC_ID}",
  "private_subnet_ids": ${PRIVATE_SUBNETS},
  "runner_image_uri": "${RUNNER_IMAGE}",
  "reports_bucket": "${REPORTS_BUCKET}"
}
EOF

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"

echo "up-loadgen.sh: pool ready"
terraform output
