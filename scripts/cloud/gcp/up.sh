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

cd "$(dirname "$0")/../../../deploy/terraform/cloud/gcp"

terraform init -upgrade >/dev/null

terraform apply -auto-approve \
  -var "release=${RELEASE}" \
  -var "project=${PROJECT}" \
  -var "region=${REGION}" \
  -var "gke_cluster_name=${CLUSTER_NAME}" \
  -var "gke_cluster_location=${CLUSTER_LOCATION}" \
  -var "namespace=${NAMESPACE}"

KMS_KEY_ID=$(terraform output -raw kms_key_id)
ARTIFACT_BUCKET=$(terraform output -raw artifact_bucket)
SA_EMAIL=$(terraform output -raw gcp_service_account_email 2>/dev/null || echo "")

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
EOF
