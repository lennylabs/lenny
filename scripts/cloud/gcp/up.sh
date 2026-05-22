#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gke/up.sh — brings up the per-release GCP resources
# the Lenny chart consumes, mirroring scripts/cloud/eks/up.sh.
#
# This script provisions the §4.5 ArtifactStore (GCS bucket), the
# §4.9 / §13.3 KEK (Cloud KMS key), and the Workload Identity
# binding (GCP service account federated to the GKE cluster's
# Kubernetes service account). The GKE cluster itself is operator-
# supplied — the Lenny Terraform module does not create cluster /
# VPC / node-pool layers (per the spec note on deployer-managed
# infrastructure).
#
# Usage:
#   GCP_PROJECT=acme-prod \
#   GCP_REGION=us-central1 \
#   GKE_CLUSTER_NAME=lenny-e2e \
#   GKE_CLUSTER_LOCATION=us-central1 \
#   scripts/cloud/gke/up.sh <release-name>
#
# Outputs the env vars the tier-6 tests read:
#   LENNY_CLOUD_PROVIDERS=gcp
#   LENNY_GCP_PROJECT
#   LENNY_GCP_REGION
#   LENNY_GCP_KMS_KEY_ID
#   LENNY_GCP_ARTIFACT_BUCKET
#   LENNY_GCP_SERVICE_ACCOUNT_EMAIL

set -euo pipefail

RELEASE="${1:-lenny-e2e}"
PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"
REGION="${GCP_REGION:-us-central1}"
CLUSTER_NAME="${GKE_CLUSTER_NAME:-}"
CLUSTER_LOCATION="${GKE_CLUSTER_LOCATION:-${REGION}}"
NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required tool '$1' is not on PATH" >&2
    exit 1
  fi
}

require gcloud
require terraform
require kubectl

TF_DIR="$(cd "$(dirname "$0")/../../../deploy/terraform/cloud/gcp" && pwd)"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

cd "${TF_DIR}"

terraform init -upgrade >/dev/null

# tfvars is the per-release topology declaration: which managed
# services to provision, their tiers, their PSA wiring. It is
# written ONCE at first up.sh and reused as-is on every later
# invocation, so re-running up.sh after a tier-6 run-e2e.sh (which
# calls up.sh without WITH_CLOUD_SQL / WITH_MEMORYSTORE set) does
# NOT destroy the managed datastores that an earlier tier-7 run
# brought up — the infra is treated as a whole. To change topology,
# run gcp/down.sh first; the next up.sh writes a fresh tfvars from
# the current env.
if [[ -f "${TFVARS_FILE}" ]]; then
  echo "gcp/up.sh: reusing existing topology declaration ${TFVARS_FILE}" >&2
  echo "  (to change topology, run gcp/down.sh first; env flags WITH_CLOUD_SQL / WITH_MEMORYSTORE / CLOUD_SQL_TIER / MEMORYSTORE_MEMORY_SIZE_GB apply only on first creation)" >&2
else
  WITH_CLOUD_SQL="${WITH_CLOUD_SQL:-false}"
  WITH_MEMORYSTORE="${WITH_MEMORYSTORE:-false}"
  CLOUD_SQL_TIER="${CLOUD_SQL_TIER:-db-custom-2-7680}"
  CLOUD_SQL_DISK_SIZE_GB="${CLOUD_SQL_DISK_SIZE_GB:-20}"
  MEMORYSTORE_TIER="${MEMORYSTORE_TIER:-STANDARD_HA}"
  MEMORYSTORE_MEMORY_SIZE_GB="${MEMORYSTORE_MEMORY_SIZE_GB:-1}"
  MANAGED_DATASTORES_NETWORK="${MANAGED_DATASTORES_NETWORK:-}"
  MANAGED_DATASTORES_AUTHORIZED_NETWORKS="${MANAGED_DATASTORES_AUTHORIZED_NETWORKS:-[]}"
  echo "gcp/up.sh: writing new topology declaration ${TFVARS_FILE}" >&2
  echo "  cloud-sql=${WITH_CLOUD_SQL} (${CLOUD_SQL_TIER}, ${CLOUD_SQL_DISK_SIZE_GB} GB)" >&2
  echo "  memorystore=${WITH_MEMORYSTORE} (${MEMORYSTORE_TIER}, ${MEMORYSTORE_MEMORY_SIZE_GB} GB)" >&2
  cat > "${TFVARS_FILE}" <<JSON
{
  "release":                              "${RELEASE}",
  "project":                              "${PROJECT}",
  "region":                               "${REGION}",
  "gke_cluster_name":                     "${CLUSTER_NAME}",
  "gke_cluster_location":                 "${CLUSTER_LOCATION}",
  "namespace":                            "${NAMESPACE}",
  "create_cloud_sql":                     ${WITH_CLOUD_SQL},
  "cloud_sql_tier":                       "${CLOUD_SQL_TIER}",
  "cloud_sql_disk_size_gb":               ${CLOUD_SQL_DISK_SIZE_GB},
  "create_memorystore":                   ${WITH_MEMORYSTORE},
  "memorystore_tier":                     "${MEMORYSTORE_TIER}",
  "memorystore_memory_size_gb":           ${MEMORYSTORE_MEMORY_SIZE_GB},
  "managed_datastores_network":           "${MANAGED_DATASTORES_NETWORK}",
  "managed_datastores_authorized_networks": ${MANAGED_DATASTORES_AUTHORIZED_NETWORKS}
}
JSON
fi

terraform apply -auto-approve -var-file="${TFVARS_FILE}"

KMS_KEY_ID=$(terraform output -raw kms_key_id)
ARTIFACT_BUCKET=$(terraform output -raw artifact_bucket)
SA_EMAIL=$(terraform output -raw gcp_service_account_email 2>/dev/null || echo "")
CLOUD_SQL_CONNECTION_NAME=$(terraform output -raw cloud_sql_connection_name 2>/dev/null || echo "")
CLOUD_SQL_PUBLIC_IP=$(terraform output -raw cloud_sql_public_ip 2>/dev/null || echo "")
CLOUD_SQL_PRIVATE_IP=$(terraform output -raw cloud_sql_private_ip 2>/dev/null || echo "")
CLOUD_SQL_ADMIN_SECRET_NAME=$(terraform output -raw cloud_sql_admin_secret_name 2>/dev/null || echo "")
CLOUD_SQL_DATABASE_NAME=$(terraform output -raw cloud_sql_database_name 2>/dev/null || echo "")
MEMORYSTORE_INSTANCE_ID=$(terraform output -raw memorystore_instance_id 2>/dev/null || echo "")
MEMORYSTORE_HOST=$(terraform output -raw memorystore_host 2>/dev/null || echo "")
MEMORYSTORE_PORT=$(terraform output -raw memorystore_port 2>/dev/null || echo "")
MEMORYSTORE_AUTH_SECRET_NAME=$(terraform output -raw memorystore_auth_secret_name 2>/dev/null || echo "")

# Resolve kubeconfig for the operator-supplied cluster.
if [[ -n "${CLUSTER_NAME}" ]]; then
  gcloud container clusters get-credentials "${CLUSTER_NAME}" \
    --project "${PROJECT}" \
    --location "${CLUSTER_LOCATION}"
fi

cat <<EOF
export LENNY_CLOUD_PROVIDERS=gcp
export LENNY_CLOUD_PROVIDER=gcp
export LENNY_GCP_PROJECT="${PROJECT}"
export LENNY_GCP_REGION="${REGION}"
export LENNY_GCP_KMS_KEY_ID="${KMS_KEY_ID}"
export LENNY_GCP_ARTIFACT_BUCKET="${ARTIFACT_BUCKET}"
export LENNY_GCP_SERVICE_ACCOUNT_EMAIL="${SA_EMAIL}"
export LENNY_GCP_CLOUD_SQL_CONNECTION_NAME="${CLOUD_SQL_CONNECTION_NAME}"
export LENNY_GCP_CLOUD_SQL_PUBLIC_IP="${CLOUD_SQL_PUBLIC_IP}"
export LENNY_GCP_CLOUD_SQL_PRIVATE_IP="${CLOUD_SQL_PRIVATE_IP}"
export LENNY_GCP_CLOUD_SQL_ADMIN_SECRET_NAME="${CLOUD_SQL_ADMIN_SECRET_NAME}"
export LENNY_GCP_CLOUD_SQL_DATABASE_NAME="${CLOUD_SQL_DATABASE_NAME}"
export LENNY_GCP_MEMORYSTORE_INSTANCE_ID="${MEMORYSTORE_INSTANCE_ID}"
export LENNY_GCP_MEMORYSTORE_HOST="${MEMORYSTORE_HOST}"
export LENNY_GCP_MEMORYSTORE_PORT="${MEMORYSTORE_PORT}"
export LENNY_GCP_MEMORYSTORE_AUTH_SECRET_NAME="${MEMORYSTORE_AUTH_SECRET_NAME}"
EOF
