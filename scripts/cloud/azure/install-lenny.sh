#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/install-lenny.sh — Helm install/upgrade of Lenny
# against an AKS cluster provisioned by up.sh. Idempotent.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
LOCATION="${AZURE_LOCATION:-eastus}"
RG="${AZURE_RESOURCE_GROUP:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

if [[ -z "${RG}" ]]; then
  echo "install-lenny.sh: AZURE_RESOURCE_GROUP is required" >&2; exit 2
fi
for cli in az kubectl helm; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "install-lenny.sh: ${cli} not on PATH" >&2; exit 3; }
done

az aks get-credentials --resource-group "${RG}" --name "${LENNY_RELEASE}-aks" >/dev/null

VALUES_FILE="$(mktemp -t lenny-load-values.yaml.XXXXXX)"
bash "${REPO_ROOT}/scripts/cloud/azure/render-values.sh" > "${VALUES_FILE}"

helm upgrade --install "${LENNY_RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace lenny-system --create-namespace \
  --values "${VALUES_FILE}" \
  --wait --timeout 10m

kubectl -n lenny-system rollout status deploy/lenny-gateway --timeout=5m
echo "install-lenny.sh: ${LENNY_RELEASE} ready"
