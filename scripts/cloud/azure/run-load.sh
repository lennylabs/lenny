#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/run-load.sh — tier-12 cloud-load trigger.
# Pre-flight + API trigger. No terraform. No helm. See the canonical
# documentation in scripts/cloud/aws/run-load.sh.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
LOCATION="${AZURE_LOCATION:-eastus}"
RG="${AZURE_RESOURCE_GROUP:-}"
SCALE="${LENNY_LOAD_SCALE:-small}"
SCENARIOS="${LENNY_SCENARIOS:-default}"

for cli in az curl jq; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "run-load.sh: ${cli} not on PATH" >&2; exit 3; }
done
if [[ -z "${RG}" ]]; then echo "AZURE_RESOURCE_GROUP required" >&2; exit 2; fi

echo "==[1/4] verifying AKS cluster" >&2
az aks show --resource-group "${RG}" --name "${LENNY_RELEASE}-aks" >/dev/null 2>&1 \
  || { echo "  FAIL: AKS ${LENNY_RELEASE}-aks not found. Run up.sh first." >&2; exit 3; }
echo "  OK"

echo "==[2/4] verifying Helm release" >&2
az aks get-credentials --resource-group "${RG}" --name "${LENNY_RELEASE}-aks" >/dev/null 2>&1
helm status "${LENNY_RELEASE}" -n lenny-system >/dev/null 2>&1 \
  || { echo "  FAIL: install-lenny.sh first" >&2; exit 3; }
echo "  OK"

echo "==[3/4] verifying loadgen VMSS" >&2
VMSS_NAME="${LENNY_RELEASE}-loadgen-runner"
CAPACITY=$(az vmss show --resource-group "${RG}" --name "${VMSS_NAME}" --query 'sku.capacity' --output tsv 2>/dev/null || echo 0)
[[ "${CAPACITY}" =~ ^[1-9] ]] || { echo "  FAIL: up-loadgen.sh first (capacity=${CAPACITY})" >&2; exit 3; }
echo "  OK (capacity=${CAPACITY})"

echo "==[4/4] triggering run scale=${SCALE} scenarios=${SCENARIOS}" >&2
LOADCTL_URL="${LENNY_LOADCTL_URL:-}"
if [[ -z "${LOADCTL_URL}" ]]; then
  LOADCTL_URL=$(az containerapp show --resource-group "${RG}" --name "${LENNY_RELEASE}-loadctl" --query 'properties.configuration.ingress.fqdn' --output tsv 2>/dev/null || echo "")
  [[ -n "${LOADCTL_URL}" ]] && LOADCTL_URL="https://${LOADCTL_URL}"
fi
[[ -n "${LOADCTL_URL}" ]] || { echo "  FAIL: up-loadctl.sh first" >&2; exit 3; }

RUN_REQ=$(jq -n --arg scale "${SCALE}" --arg sc "${SCENARIOS}" --arg release "${LENNY_RELEASE}" \
  '{scale: $scale, scenarios: ($sc | split(",")), cluster_release: $release}')
RUN_ID=$(curl -fsS -X POST "${LOADCTL_URL}/api/v1/runs" \
  -H "Authorization: Bearer ${LENNY_OIDC_TOKEN:-}" -H "Content-Type: application/json" \
  -d "${RUN_REQ}" | jq -r '.id' 2>/dev/null || echo "")
[[ -n "${RUN_ID}" && "${RUN_ID}" != "null" ]] || { echo "  FAIL: POST /api/v1/runs (Wave 6 wires real endpoint)" >&2; exit 1; }
echo "  run_id=${RUN_ID}"

while true; do
  STATUS=$(curl -fsS "${LOADCTL_URL}/api/v1/runs/${RUN_ID}" -H "Authorization: Bearer ${LENNY_OIDC_TOKEN:-}" | jq -r '.status')
  case "${STATUS}" in
    PASS) echo "run-load.sh: PASS"; exit 0 ;;
    FAIL|ABORTED) echo "run-load.sh: ${STATUS}"; exit 1 ;;
    PENDING|RUNNING) sleep 15 ;;
    *) echo "run-load.sh: unexpected ${STATUS}" >&2; exit 1 ;;
  esac
done
