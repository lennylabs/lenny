#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aks/up.sh — brings up the per-release Azure resources
# the Lenny chart consumes, mirroring scripts/cloud/eks/up.sh.
#
# Provisions the §4.5 ArtifactStore (Blob Storage container), the
# §4.9 / §13.3 KEK (Key Vault key), and the Workload Identity
# binding (user-assigned managed identity federated to the AKS
# cluster's Kubernetes service account). The AKS cluster itself is
# operator-supplied — the Lenny Terraform module does not create the
# cluster / vnet / node-pool layers.
#
# Usage:
#   AZURE_SUBSCRIPTION_ID=... \
#   AZURE_RESOURCE_GROUP=lenny-e2e \
#   AZURE_LOCATION=eastus \
#   AKS_CLUSTER_NAME=lenny-e2e \
#   scripts/cloud/aks/up.sh <release-name>
#
# Outputs the env vars the tier-6 tests read:
#   LENNY_CLOUD_PROVIDER=aks
#   LENNY_AZURE_RESOURCE_GROUP
#   LENNY_AZURE_LOCATION
#   LENNY_AZURE_KEY_VAULT_KEY_ID
#   LENNY_AZURE_ARTIFACT_CONTAINER_URL
#   LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID

set -euo pipefail

RELEASE="${1:-lenny-e2e}"
SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID:?AZURE_SUBSCRIPTION_ID must be set}"
RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"
LOCATION="${AZURE_LOCATION:-eastus}"
CLUSTER_NAME="${AKS_CLUSTER_NAME:-}"
NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required tool '$1' is not on PATH" >&2
    exit 1
  fi
}

require az
require terraform
require kubectl

# Resolve OIDC issuer URL for the cluster (needed to bind the
# federated identity to the cluster's SA).
OIDC_ISSUER_URL=""
if [[ -n "${CLUSTER_NAME}" ]]; then
  OIDC_ISSUER_URL=$(az aks show \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --query "oidcIssuerProfile.issuerUrl" \
    --output tsv)
fi

cd "$(dirname "$0")/../../../deploy/terraform/cloud/azure"

terraform init -upgrade >/dev/null

terraform apply -auto-approve \
  -var "release=${RELEASE}" \
  -var "resource_group=${RESOURCE_GROUP}" \
  -var "location=${LOCATION}" \
  -var "aks_oidc_issuer_url=${OIDC_ISSUER_URL}" \
  -var "namespace=${NAMESPACE}"

KEY_VAULT_KEY_ID=$(terraform output -raw key_vault_key_id)
ARTIFACT_CONTAINER_URL=$(terraform output -raw artifact_container_url)
WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw workload_identity_client_id 2>/dev/null || echo "")

# Resolve kubeconfig for the operator-supplied cluster.
if [[ -n "${CLUSTER_NAME}" ]]; then
  az aks get-credentials \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --overwrite-existing
fi

cat <<EOF
export LENNY_CLOUD_PROVIDER=aks
export LENNY_AZURE_RESOURCE_GROUP="${RESOURCE_GROUP}"
export LENNY_AZURE_LOCATION="${LOCATION}"
export LENNY_AZURE_KEY_VAULT_KEY_ID="${KEY_VAULT_KEY_ID}"
export LENNY_AZURE_ARTIFACT_CONTAINER_URL="${ARTIFACT_CONTAINER_URL}"
export LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID="${WORKLOAD_IDENTITY_CLIENT_ID}"
EOF
