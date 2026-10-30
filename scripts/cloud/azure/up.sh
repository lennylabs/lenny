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

TF_DIR="$(cd "$(dirname "$0")/../../../deploy/terraform/cloud/azure" && pwd)"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

cd "${TF_DIR}"

terraform init -upgrade >/dev/null

# tfvars is the per-release topology declaration: which managed
# services to provision, their SKUs, their networking. It is written
# ONCE at first up.sh and reused as-is on every later invocation,
# so re-running up.sh after a tier-6 run-e2e.sh (which calls up.sh
# without WITH_FLEXIBLE_POSTGRES / WITH_AZURE_REDIS set) does NOT
# destroy the managed datastores that an earlier tier-7 run brought
# up — the infra is treated as a whole. To change topology, run
# azure/down.sh first; the next up.sh writes a fresh tfvars from the
# current env.
if [[ -f "${TFVARS_FILE}" ]]; then
  echo "azure/up.sh: reusing existing topology declaration ${TFVARS_FILE}" >&2
  echo "  (to change topology, run azure/down.sh first; env flags WITH_FLEXIBLE_POSTGRES / WITH_AZURE_REDIS / *_SKU apply only on first creation)" >&2
else
  WITH_FLEXIBLE_POSTGRES="${WITH_FLEXIBLE_POSTGRES:-false}"
  WITH_AZURE_REDIS="${WITH_AZURE_REDIS:-false}"
  # Tier-7 sizing knobs. Defaults match the terraform module's
  # tier-6 baseline; tier-7 run-load.sh maps LENNY_LOAD_SCALE into
  # these so the SKU aligns with the load envelope.
  FLEXIBLE_POSTGRES_SKU="${FLEXIBLE_POSTGRES_SKU:-B_Standard_B2s}"
  FLEXIBLE_POSTGRES_STORAGE_MB="${FLEXIBLE_POSTGRES_STORAGE_MB:-32768}"
  AZURE_REDIS_SKU="${AZURE_REDIS_SKU:-Standard}"
  AZURE_REDIS_FAMILY="${AZURE_REDIS_FAMILY:-C}"
  AZURE_REDIS_CAPACITY="${AZURE_REDIS_CAPACITY:-1}"
  CALLER_IP="${MANAGED_DATASTORES_CALLER_IP:-}"
  echo "azure/up.sh: writing new topology declaration ${TFVARS_FILE}" >&2
  echo "  flexible-postgres=${WITH_FLEXIBLE_POSTGRES} (${FLEXIBLE_POSTGRES_SKU}, ${FLEXIBLE_POSTGRES_STORAGE_MB} MB)" >&2
  echo "  redis=${WITH_AZURE_REDIS} (${AZURE_REDIS_SKU} ${AZURE_REDIS_FAMILY}${AZURE_REDIS_CAPACITY})" >&2
  cat > "${TFVARS_FILE}" <<JSON
{
  "release":            "${RELEASE}",
  "resource_group":     "${RESOURCE_GROUP}",
  "location":           "${LOCATION}",
  "aks_oidc_issuer_url": "${OIDC_ISSUER_URL}",
  "namespace":          "${NAMESPACE}",
  "create_flexible_postgres":        ${WITH_FLEXIBLE_POSTGRES},
  "flexible_postgres_sku":           "${FLEXIBLE_POSTGRES_SKU}",
  "flexible_postgres_storage_mb":    ${FLEXIBLE_POSTGRES_STORAGE_MB},
  "create_azure_redis":              ${WITH_AZURE_REDIS},
  "azure_redis_sku":                 "${AZURE_REDIS_SKU}",
  "azure_redis_family":              "${AZURE_REDIS_FAMILY}",
  "azure_redis_capacity":            ${AZURE_REDIS_CAPACITY},
  "managed_datastores_caller_ip":    "${CALLER_IP}"
}
JSON
fi

terraform apply -auto-approve -var-file="${TFVARS_FILE}"

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
