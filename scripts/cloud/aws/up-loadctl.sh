#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/up-loadctl.sh — provisions the tier-12 control
# plane on AWS (Fargate + ALB + RDS + IAM). Idempotent.
#
# Required environment:
#
#   AWS_PROFILE                       — SSO profile.
#   AWS_REGION                        — target region.
#   LENNY_RELEASE                     — release name.
#   LENNY_LOADCTL_DB_PASSWORD         — admin password for the loadctl RDS.
#   LENNY_TLS_CERTIFICATE_ARN         — ACM cert ARN for the ALB.
#   LENNY_LOADCTL_OPERATOR_TOKENS     — comma-separated operator bearer tokens.
#   LENNY_LOADCTL_RUNNER_TOKENS       — comma-separated runner bearer tokens.
#
# Optional:
#
#   LENNY_LOADCTL_PROGRESS_DIR        — persistent-sink URL (s3://… or file:///…).
#   LENNY_LOADCTL_RUN_DURATION        — per-scenario duration (e.g. "60s").
#   LENNY_LOADCTL_RL_RUNS_PER_MIN     — rate limit on POST /api/v1/runs.
#   LENNY_LOADCTL_RL_PROGRESS_PER_SEC — rate limit on POST /api/v1/progress.
#   LENNY_LOADCTL_RL_ACK_PER_SEC      — rate limit on POST /api/v1/ack.
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
if [[ -z "${LENNY_LOADCTL_OPERATOR_TOKENS:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_OPERATOR_TOKENS is required" >&2; exit 2
fi
if [[ -z "${LENNY_LOADCTL_RUNNER_TOKENS:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_RUNNER_TOKENS is required" >&2; exit 2
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

# Read loadgen queue ARN + URL from the loadgen module.
LOADGEN_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws/loadgen"
cd "${LOADGEN_TF_DIR}"
LOADGEN_QUEUE_ARN="$(terraform output -raw queue_arn 2>/dev/null || true)"
LOADGEN_QUEUE_URL="$(terraform output -raw queue_url 2>/dev/null || true)"
if [[ -z "${LOADGEN_QUEUE_ARN}" || -z "${LOADGEN_QUEUE_URL}" ]]; then
  echo "up-loadctl.sh: queue_arn / queue_url output missing from ${LOADGEN_TF_DIR}; run up-loadgen.sh first" >&2; exit 4
fi

REPORTS_BUCKET="${LENNY_RELEASE}-load-reports"
LOADCTL_IMAGE="$(aws sts get-caller-identity --query Account --output text).dkr.ecr.${REGION}.amazonaws.com/lenny-loadctl:latest"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE VPC_ID PRIVATE_SUBNETS PUBLIC_SUBNETS LOADCTL_IMAGE \
       LOADGEN_QUEUE_ARN LOADGEN_QUEUE_URL REPORTS_BUCKET \
       LENNY_LOADCTL_DB_PASSWORD LENNY_TLS_CERTIFICATE_ARN \
       LENNY_LOADCTL_OPERATOR_TOKENS LENNY_LOADCTL_RUNNER_TOKENS
python3 - "$TFVARS_FILE" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "vpc_id": os.environ["VPC_ID"],
    "private_subnet_ids": json.loads(os.environ["PRIVATE_SUBNETS"]),
    "public_subnet_ids": json.loads(os.environ["PUBLIC_SUBNETS"]),
    "loadctl_image_uri": os.environ["LOADCTL_IMAGE"],
    "loadgen_queue_arn": os.environ["LOADGEN_QUEUE_ARN"],
    "loadgen_queue_url": os.environ["LOADGEN_QUEUE_URL"],
    "reports_bucket": os.environ["REPORTS_BUCKET"],
    "db_username": "lenny",
    "db_password": os.environ["LENNY_LOADCTL_DB_PASSWORD"],
    "tls_certificate_arn": os.environ["LENNY_TLS_CERTIFICATE_ARN"],
    "operator_tokens": os.environ["LENNY_LOADCTL_OPERATOR_TOKENS"],
    "runner_tokens": os.environ["LENNY_LOADCTL_RUNNER_TOKENS"],
    "progress_dir": os.environ.get("LENNY_LOADCTL_PROGRESS_DIR", ""),
    "run_duration": os.environ.get("LENNY_LOADCTL_RUN_DURATION", ""),
    "ratelimit_runs_per_min": int(os.environ.get("LENNY_LOADCTL_RL_RUNS_PER_MIN", "0")),
    "ratelimit_progress_per_sec": int(os.environ.get("LENNY_LOADCTL_RL_PROGRESS_PER_SEC", "0")),
    "ratelimit_ack_per_sec": int(os.environ.get("LENNY_LOADCTL_RL_ACK_PER_SEC", "0")),
}
with open(sys.argv[1], "w") as f:
    json.dump(out, f, indent=2)
PY

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"
echo "up-loadctl.sh: control plane ready"
terraform output
