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
#   LENNY_CLOUD_PROVIDERS=azure
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

# Tier-7 managed datastores. WITH_FLEXIBLE_POSTGRES=1 +
# WITH_AZURE_REDIS=1 (the Azure equivalents of WITH_RDS /
# WITH_ELASTICACHE on AWS) provision Azure Database for PostgreSQL
# Flexible Server and Azure Cache for Redis alongside the chart
# resources, and persist their credentials in Key Vault. The default
# tier-6 e2e path leaves both gates off so the existing in-cluster
# datastore fixtures keep being used.
WITH_FLEXIBLE_POSTGRES="${WITH_FLEXIBLE_POSTGRES:-false}"
WITH_AZURE_REDIS="${WITH_AZURE_REDIS:-false}"
# Caller IP, when known, gets a firewall rule on the Flexible Server
# so the operator workstation can connect for ad-hoc DSN debugging.
CALLER_IP="${MANAGED_DATASTORES_CALLER_IP:-}"

terraform apply -auto-approve \
  -var "release=${RELEASE}" \
  -var "resource_group=${RESOURCE_GROUP}" \
  -var "location=${LOCATION}" \
  -var "aks_oidc_issuer_url=${OIDC_ISSUER_URL}" \
  -var "namespace=${NAMESPACE}" \
  -var "create_flexible_postgres=${WITH_FLEXIBLE_POSTGRES}" \
  -var "create_azure_redis=${WITH_AZURE_REDIS}" \
  -var "managed_datastores_caller_ip=${CALLER_IP}"

KEY_VAULT_KEY_ID=$(terraform output -raw key_vault_key_id)
ARTIFACT_CONTAINER_URL=$(terraform output -raw artifact_container_url)
WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw workload_identity_client_id 2>/dev/null || echo "")
FLEXIBLE_POSTGRES_FQDN=$(terraform output -raw flexible_postgres_fqdn 2>/dev/null || echo "")
FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME=$(terraform output -raw flexible_postgres_admin_secret_name 2>/dev/null || echo "")
FLEXIBLE_POSTGRES_DATABASE_NAME=$(terraform output -raw flexible_postgres_database_name 2>/dev/null || echo "")
AZURE_REDIS_HOSTNAME=$(terraform output -raw azure_redis_hostname 2>/dev/null || echo "")
AZURE_REDIS_SSL_PORT=$(terraform output -raw azure_redis_ssl_port 2>/dev/null || echo "")
AZURE_REDIS_AUTH_SECRET_NAME=$(terraform output -raw azure_redis_auth_secret_name 2>/dev/null || echo "")
KEY_VAULT_NAME=$(terraform output -raw key_vault_key_id | awk -F'/' '{print $3}' | awk -F'.' '{print $1}')

# Resolve kubeconfig for the operator-supplied cluster.
if [[ -n "${CLUSTER_NAME}" ]]; then
  az aks get-credentials \
    --resource-group "${RESOURCE_GROUP}" \
    --name "${CLUSTER_NAME}" \
    --overwrite-existing
fi

cat <<EOF
export LENNY_CLOUD_PROVIDERS=azure
export LENNY_CLOUD_PROVIDER=azure
export LENNY_AZURE_RESOURCE_GROUP="${RESOURCE_GROUP}"
export LENNY_AZURE_LOCATION="${LOCATION}"
export LENNY_AZURE_KEY_VAULT_KEY_ID="${KEY_VAULT_KEY_ID}"
export LENNY_AZURE_KEY_VAULT_NAME="${KEY_VAULT_NAME}"
export LENNY_AZURE_ARTIFACT_CONTAINER_URL="${ARTIFACT_CONTAINER_URL}"
export LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID="${WORKLOAD_IDENTITY_CLIENT_ID}"
export LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN="${FLEXIBLE_POSTGRES_FQDN}"
export LENNY_AZURE_FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME="${FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME}"
export LENNY_AZURE_FLEXIBLE_POSTGRES_DATABASE_NAME="${FLEXIBLE_POSTGRES_DATABASE_NAME}"
export LENNY_AZURE_REDIS_HOSTNAME="${AZURE_REDIS_HOSTNAME}"
export LENNY_AZURE_REDIS_SSL_PORT="${AZURE_REDIS_SSL_PORT}"
export LENNY_AZURE_REDIS_AUTH_SECRET_NAME="${AZURE_REDIS_AUTH_SECRET_NAME}"
EOF
