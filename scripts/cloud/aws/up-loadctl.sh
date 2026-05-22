#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/up-loadctl.sh — provisions the tier-12 control
# plane on AWS (Fargate + ALB + RDS + IAM). Idempotent.
#
# Required environment:
#
#   AWS_PROFILE                   — SSO profile.
#   AWS_REGION                    — target region.
#   LENNY_RELEASE                 — release name.
#   LENNY_LOADCTL_DB_PASSWORD     — admin password for the loadctl RDS.
#   LENNY_TLS_CERTIFICATE_ARN     — ACM cert ARN for the ALB.
#
# TESTING.md §12.12 / Wave 6.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${AWS_REGION:-us-west-2}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadctl"

if [[ -z "${LENNY_LOADCTL_DB_PASSWORD:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_DB_PASSWORD is required" >&2; exit 2
fi
if [[ -z "${LENNY_TLS_CERTIFICATE_ARN:-}" ]]; then
  echo "up-loadctl.sh: LENNY_TLS_CERTIFICATE_ARN is required" >&2; exit 2
fi
for cli in aws terraform; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "${cli} not on PATH" >&2; exit 3; }
done

# Read VPC + subnet IDs from the EKS up.sh terraform outputs.
EKS_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
cd "${EKS_TF_DIR}"
VPC_ID="$(terraform output -raw vpc_id 2>/dev/null || true)"
PRIVATE_SUBNETS="$(terraform output -json private_subnet_ids 2>/dev/null || echo '[]')"
PUBLIC_SUBNETS="$(terraform output -json public_subnet_ids 2>/dev/null || echo '[]')"
if [[ -z "${VPC_ID}" ]]; then
  echo "up-loadctl.sh: vpc_id output missing from ${EKS_TF_DIR}; run up.sh first" >&2; exit 4
fi

# Read loadgen queue ARN from the loadgen module.
LOADGEN_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadgen"
cd "${LOADGEN_TF_DIR}"
LOADGEN_QUEUE_ARN="$(terraform output -raw queue_arn 2>/dev/null || true)"
if [[ -z "${LOADGEN_QUEUE_ARN}" ]]; then
  echo "up-loadctl.sh: queue_arn output missing from ${LOADGEN_TF_DIR}; run up-loadgen.sh first" >&2; exit 4
fi

REPORTS_BUCKET="${LENNY_RELEASE}-load-reports"
LOADCTL_IMAGE="$(aws sts get-caller-identity --query Account --output text).dkr.ecr.${REGION}.amazonaws.com/lenny-loadctl:latest"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
cat > "${TFVARS_FILE}" <<EOF
{
  "release": "${LENNY_RELEASE}",
  "vpc_id": "${VPC_ID}",
  "private_subnet_ids": ${PRIVATE_SUBNETS},
  "public_subnet_ids": ${PUBLIC_SUBNETS},
  "loadctl_image_uri": "${LOADCTL_IMAGE}",
  "loadgen_queue_arn": "${LOADGEN_QUEUE_ARN}",
  "reports_bucket": "${REPORTS_BUCKET}",
  "db_username": "lenny",
  "db_password": "${LENNY_LOADCTL_DB_PASSWORD}",
  "tls_certificate_arn": "${LENNY_TLS_CERTIFICATE_ARN}"
}
EOF

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"
echo "up-loadctl.sh: control plane ready"
terraform output
