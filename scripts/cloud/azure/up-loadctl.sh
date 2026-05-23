#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/up-loadctl.sh — provisions the tier-12 control
# plane on Azure (Container Apps + Postgres Flexible Server). Idempotent.
#
# Required environment:
#
#   AZURE_LOCATION                    — target region (default eastus).
#   AZURE_RESOURCE_GROUP              — resource group (must match up.sh).
#   LENNY_RELEASE                     — release name.
#   LENNY_LOADCTL_DB_PASSWORD         — Postgres admin password.
#   LENNY_LOADCTL_OPERATOR_TOKENS     — comma-separated operator bearer tokens.
#   LENNY_LOADCTL_RUNNER_TOKENS       — comma-separated runner bearer tokens.
#
# Optional:
#
#   LENNY_LOADCTL_PROGRESS_DIR        — persistent-sink URL.
#   LENNY_LOADCTL_RUN_DURATION        — per-scenario duration (e.g. "60s").
#   LENNY_LOADCTL_RL_RUNS_PER_MIN     — rate limit on POST /api/v1/runs.
#   LENNY_LOADCTL_RL_PROGRESS_PER_SEC — rate limit on POST /api/v1/progress.
#   LENNY_LOADCTL_RL_ACK_PER_SEC      — rate limit on POST /api/v1/ack.
#
# Prerequisites:
#   - up.sh (AKS) and up-loadgen.sh have been applied.
#
# TESTING.md §12.12 / Wave 6.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
LOCATION="${AZURE_LOCATION:-eastus}"
RG="${AZURE_RESOURCE_GROUP:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure/loadctl"

if [[ -z "${RG}" ]]; then
  echo "up-loadctl.sh: AZURE_RESOURCE_GROUP is required" >&2; exit 2
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
for cli in az terraform; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "${cli} not on PATH" >&2; exit 3; }
done

AKS_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure"
cd "${AKS_TF_DIR}"
STORAGE_ACCOUNT="$(terraform output -raw storage_account_name 2>/dev/null || true)"
if [[ -z "${STORAGE_ACCOUNT}" ]]; then
  echo "up-loadctl.sh: storage_account_name output missing from ${AKS_TF_DIR}; run up.sh first" >&2; exit 4
fi

LOADGEN_TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/azure/loadgen"
cd "${LOADGEN_TF_DIR}"
LOADGEN_QUEUE_ID="$(terraform output -raw servicebus_queue_id 2>/dev/null || true)"
LOADGEN_QUEUE_URL="$(terraform output -raw servicebus_queue_url 2>/dev/null || true)"
if [[ -z "${LOADGEN_QUEUE_ID}" || -z "${LOADGEN_QUEUE_URL}" ]]; then
  echo "up-loadctl.sh: servicebus_queue_id / servicebus_queue_url output missing from ${LOADGEN_TF_DIR}; run up-loadgen.sh first" >&2; exit 4
fi

REPORTS_CONTAINER="${LENNY_RELEASE}-load-reports"
REPORTS_STORAGE_ID="/subscriptions/$(az account show --query id -o tsv)/resourceGroups/${RG}/providers/Microsoft.Storage/storageAccounts/${STORAGE_ACCOUNT}/blobServices/default/containers/${REPORTS_CONTAINER}"
REPORTS_STORAGE_URL="azureblob://${STORAGE_ACCOUNT}/${REPORTS_CONTAINER}"
LOADCTL_IMAGE="${LENNY_LOADCTL_IMAGE:-lenny.azurecr.io/lenny-loadctl:latest}"

TFVARS_FILE="${TF_DIR}/${LENNY_RELEASE}.tfvars.json"
export LENNY_RELEASE RG LOCATION LOADCTL_IMAGE LOADGEN_QUEUE_ID LOADGEN_QUEUE_URL \
       REPORTS_STORAGE_ID REPORTS_STORAGE_URL LENNY_LOADCTL_DB_PASSWORD \
       LENNY_LOADCTL_OPERATOR_TOKENS LENNY_LOADCTL_RUNNER_TOKENS
python3 - "${TFVARS_FILE}" <<'PY'
import json, os, sys
out = {
    "release": os.environ["LENNY_RELEASE"],
    "resource_group_name": os.environ["RG"],
    "location": os.environ["LOCATION"],
    "loadctl_image": os.environ["LOADCTL_IMAGE"],
    "loadgen_queue_id": os.environ["LOADGEN_QUEUE_ID"],
    "loadgen_queue_url": os.environ["LOADGEN_QUEUE_URL"],
    "reports_storage_id": os.environ["REPORTS_STORAGE_ID"],
    "reports_storage_url": os.environ["REPORTS_STORAGE_URL"],
    "db_admin_user": "lenny",
    "db_admin_password": os.environ["LENNY_LOADCTL_DB_PASSWORD"],
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
