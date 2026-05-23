#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/up-loadctl.sh — provisions the tier-12 control
# plane on GCP (Cloud Run + Cloud SQL + Secret Manager). Idempotent.
#
# Required environment:
#
#   GCP_PROJECT                       — target project id.
#   GCP_REGION                        — target region (default us-central1).
#   LENNY_RELEASE                     — release name.
#   LENNY_LOADCTL_DB_PASSWORD         — Cloud SQL admin password.
#   LENNY_LOADCTL_OPERATOR_TOKENS     — comma-separated operator bearer tokens.
#   LENNY_LOADCTL_RUNNER_TOKENS       — comma-separated runner bearer tokens.
#
# Optional:
#
#   LENNY_LOADCTL_PROGRESS_DIR        — persistent-sink URL (gs://… or file:///…).
#   LENNY_LOADCTL_RUN_DURATION        — per-scenario duration (e.g. "60s").
#   LENNY_LOADCTL_RL_RUNS_PER_MIN     — rate limit on POST /api/v1/runs.
#   LENNY_LOADCTL_RL_PROGRESS_PER_SEC — rate limit on POST /api/v1/progress.
#   LENNY_LOADCTL_RL_ACK_PER_SEC      — rate limit on POST /api/v1/ack.
#
# Prerequisites:
#   - up.sh (GKE) and up-loadgen.sh have been applied.
#
# TESTING.md §12.12 / Wave 6.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${GCP_REGION:-us-central1}"
PROJECT_ID="${GCP_PROJECT:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp/loadctl"

if [[ -z "${PROJECT_ID}" ]]; then
  echo "up-loadctl.sh: GCP_PROJECT is required" >&2; exit 2
fi
if [[ -z "${LENNY_LOADCTL_DB_PASSWORD:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_DB_PASSWORD is required" >&2; exit 2
fi
if [[ -z "${LENNY_LOADCTL_OPERATOR_TOKENS:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_OPERATOR_TOKENS is required" >&2; exit 2
fi
if [[ -z "${LENNY_LOADCTL_RUNNER_TOKENS:-}" ]]; then
  echo "up-loadctl.sh: LENNY_LOADCTL_RUNNER_TOKENS is required" >&2; exit 2
fi
for cli in gcloud terraform; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "${cli} not on PATH" >&2; exit 3; }
done

# Read GKE network + VPC connector from the up.sh terraform outputs.
GKE_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp"
cd "${GKE_TF_DIR}"
VPC_CONNECTOR="$(terraform output -raw loadctl_vpc_connector_id 2>/dev/null || true)"
if [[ -z "${VPC_CONNECTOR}" ]]; then
  echo "up-loadctl.sh: loadctl_vpc_connector_id output missing from ${GKE_TF_DIR}; run up.sh first" >&2; exit 4
fi

# Read Pub/Sub topic from the loadgen module.
LOADGEN_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/gcp/loadgen"
cd "${LOADGEN_TF_DIR}"
LOADGEN_TOPIC="$(terraform output -raw topic_name 2>/dev/null || true)"
if [[ -z "${LOADGEN_TOPIC}" ]]; then
  echo "up-loadctl.sh: topic_name output missing from ${LOADGEN_TF_DIR}; run up-loadgen.sh first" >&2; exit 4
fi

REPORTS_BUCKET="${LENNY_RELEASE}-load-reports"
LOADCTL_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/lenny/lenny-loadctl:latest"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE PROJECT_ID REGION LOADCTL_IMAGE LOADGEN_TOPIC \
       REPORTS_BUCKET VPC_CONNECTOR LENNY_LOADCTL_DB_PASSWORD \
       LENNY_LOADCTL_OPERATOR_TOKENS LENNY_LOADCTL_RUNNER_TOKENS
python3 - "${TFVARS_FILE}" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "project_id": os.environ["PROJECT_ID"],
    "region": os.environ["REGION"],
    "loadctl_image": os.environ["LOADCTL_IMAGE"],
    "loadgen_topic": os.environ["LOADGEN_TOPIC"],
    "reports_bucket": os.environ["REPORTS_BUCKET"],
    "vpc_connector_id": os.environ["VPC_CONNECTOR"],
    "db_password": os.environ["LENNY_LOADCTL_DB_PASSWORD"],
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
