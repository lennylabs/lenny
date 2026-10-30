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
#   AWS_PROFILE                    — SSO profile.
#   AWS_REGION                     — target region (default us-west-2).
#   LENNY_RELEASE                  — release name (must match the EKS up.sh release).
#   LENNY_LOADCTL_URL              — base URL of the deployed loadctl
#                                    (output of up-loadctl.sh).
#   LENNY_LOADRUNNER_TOKEN         — bearer token the runner sends with every
#                                    loadctl callback. Must appear in the
#                                    LENNY_LOADCTL_RUNNER_TOKENS list.
#
# Optional:
#
#   LENNY_LOADRUNNER_REPORT_STORAGE_URL — object-storage URL the runner
#                                         uploads per-scenario k6 summaries
#                                         to. Default: s3://${reports_bucket}.
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

if [[ -z "${LENNY_LOADCTL_URL:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADCTL_URL is required (run up-loadctl.sh first; export service_url)" >&2
  exit 2
fi
if [[ -z "${LENNY_LOADRUNNER_TOKEN:-}" ]]; then
  echo "up-loadgen.sh: LENNY_LOADRUNNER_TOKEN is required" >&2
  exit 2
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
REPORT_STORAGE_URL="${LENNY_LOADRUNNER_REPORT_STORAGE_URL:-s3://${REPORTS_BUCKET}}"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE VPC_ID PRIVATE_SUBNETS RUNNER_IMAGE REPORTS_BUCKET \
       LENNY_LOADCTL_URL LENNY_LOADRUNNER_TOKEN REPORT_STORAGE_URL
python3 - "${TFVARS_FILE}" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "vpc_id": os.environ["VPC_ID"],
    "private_subnet_ids": json.loads(os.environ["PRIVATE_SUBNETS"]),
    "runner_image_uri": os.environ["RUNNER_IMAGE"],
    "reports_bucket": os.environ["REPORTS_BUCKET"],
    "loadctl_url": os.environ["LENNY_LOADCTL_URL"],
    "runner_token": os.environ["LENNY_LOADRUNNER_TOKEN"],
    "report_storage_url": os.environ["REPORT_STORAGE_URL"],
}
with open(sys.argv[1], "w") as f:
    json.dump(out, f, indent=2)
PY

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"

echo "up-loadgen.sh: pool ready"
terraform output
