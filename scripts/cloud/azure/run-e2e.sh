#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/run-e2e.sh — end-to-end driver for a tier-6
# AKS verification run. Mirrors scripts/cloud/aws/run-e2e.sh with
# the Azure equivalents: Key Vault, Blob Storage, Azure Workload
# Identity, and Azure Container Registry (in lieu of ECR).
#
# Sequence:
#
#   1. scripts/cloud/azure/up.sh      — terraform apply (Key Vault +
#                                       Blob + Workload Identity).
#   2. docker push images to ACR (operator-supplied).
#   3. helm template lenny chart with the Azure values overlay.
#   4. kubectl apply -f tests/testinfra/k8s/datastores.yaml
#                                     — Postgres + Redis fixtures
#                                       inside the cluster (Blob
#                                       replaces MinIO).
#   5. helm install lenny-e2e         — chart install with the Azure
#                                       overlay; wait for ready.
#   6. lenny-test --tier e2e_cloud    — run the tier-6 suite.
#   7. scripts/cloud/azure/down.sh    — terraform destroy on exit
#                                       (unless KEEP_CLUSTER=1).
#
# Required environment:
#
#   AZURE_SUBSCRIPTION_ID        — the subscription the resources live in.
#   AZURE_RESOURCE_GROUP         — the resource group hosting everything.
#   AZURE_LOCATION               — default eastus.
#   AKS_CLUSTER_NAME             — operator-supplied AKS cluster.
#
# Optional environment:
#
#   LENNY_RELEASE                — Helm release name + Terraform prefix.
#                                  Default lenny-e2e.
#   IMAGE_TAG                    — image tag (default 0.1.0).
#   KEEP_CLUSTER=1               — default. Skip terraform destroy on exit.
#   LENNY_NAMESPACE              — chart namespace. Default lenny-system.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-e2e}"
IMAGE_TAG="${IMAGE_TAG:-0.1.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"
LENNY_NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"

AZURE_SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID:?AZURE_SUBSCRIPTION_ID must be set}"
AZURE_RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"
AZURE_LOCATION="${AZURE_LOCATION:-eastus}"
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:?AKS_CLUSTER_NAME must be set}"

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"

log() { printf '==> %s\n' "$*"; }

trap_exit() {
  if [[ "${KEEP_CLUSTER}" != "1" ]]; then
    log "tearing down Azure resources (KEEP_CLUSTER!=1)"
    "${REPO_ROOT}/scripts/cloud/azure/down.sh" || true
  fi
}
trap trap_exit EXIT

log "step 1: scripts/cloud/azure/up.sh"
ENV_OUT=$(AZURE_SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID}" \
  AZURE_RESOURCE_GROUP="${AZURE_RESOURCE_GROUP}" \
  AZURE_LOCATION="${AZURE_LOCATION}" \
  AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME}" \
  LENNY_NAMESPACE="${LENNY_NAMESPACE}" \
  "${REPO_ROOT}/scripts/cloud/azure/up.sh" "${LENNY_RELEASE}")
eval "${ENV_OUT}"

log "step 2: Push Lenny images to ACR (operator-supplied)"
log "  set DOCKER_REGISTRY=<acr-name>.azurecr.io"
log "  and run scripts/cloud/aws/build-images.sh equivalents."

log "step 3: render Helm values overlay"
cat > "${REPO_ROOT}/scripts/cloud/azure/values-cloud-azure.${LENNY_RELEASE}.yaml" <<EOF
controller:
  egressCaptureImage: ""
gateway:
  serviceAccount:
    annotations:
      azure.workload.identity/client-id: ${LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID}
  podAnnotations:
    azure.workload.identity/use: "true"
features:
  llmProxy: true
EOF

log "step 4: in-cluster Postgres + Redis fixtures (or Azure DB / Cache)"
kubectl apply -f "${REPO_ROOT}/tests/testinfra/k8s/datastores.yaml"

log "step 5: helm install ${LENNY_RELEASE}"
helm upgrade --install "${LENNY_RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace "${LENNY_NAMESPACE}" --create-namespace \
  --values "${REPO_ROOT}/scripts/cloud/azure/values-cloud-azure.${LENNY_RELEASE}.yaml" \
  --wait --timeout 10m

log "step 6: run tier-6 suite"
cd "${REPO_ROOT}"
LENNY_CLOUD_PROVIDER=azure \
LENNY_AZURE_RESOURCE_GROUP="${LENNY_AZURE_RESOURCE_GROUP}" \
LENNY_AZURE_LOCATION="${LENNY_AZURE_LOCATION}" \
LENNY_AZURE_KEY_VAULT_KEY_ID="${LENNY_AZURE_KEY_VAULT_KEY_ID}" \
LENNY_AZURE_ARTIFACT_CONTAINER_URL="${LENNY_AZURE_ARTIFACT_CONTAINER_URL}" \
lenny-test --tier e2e_cloud --output human

log "tier-6 AKS run complete; KEEP_CLUSTER=${KEEP_CLUSTER}"
