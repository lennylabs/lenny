#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/azure/down-runtimeclass-pools.sh — tears down the gVisor
# and Kata node pools, RuntimeClasses, and SDK-warm pools that
# up-runtimeclass-pools.sh created. Tolerates missing targets.
#
# Usage:
#   AZURE_RESOURCE_GROUP=lenny-load AKS_CLUSTER_NAME=lenny-load-small-aks \
#   LENNY_BENCH_RUNTIME_CLASSES=gvisor,kata \
#   scripts/cloud/azure/down-runtimeclass-pools.sh

set -euo pipefail

RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${AKS_CLUSTER_NAME:-${RELEASE}-aks}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-gvisor,kata}"

for cli in az kubectl; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "down-runtimeclass-pools.sh: ${cli} not on PATH" >&2; exit 3; }
done

log() { printf '==> %s\n' "$*" >&2; }

has_class() {
  local want="$1"
  local IFS=','
  for c in ${CLASSES}; do
    [[ "${c// /}" == "${want}" ]] && return 0
  done
  return 1
}

delete_node_pool() {
  az aks nodepool delete --resource-group "${RESOURCE_GROUP}" --cluster-name "${CLUSTER}" --name "$1" 2>/dev/null || true
}

if has_class gvisor; then
  log "removing gVisor SDK-warm pool + RuntimeClass + node pool"
  kubectl delete sandboxwarmpool load-preconnect-pool-gvisor -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-gvisor -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-gvisor --ignore-not-found
  kubectl delete runtimeclass gvisor --ignore-not-found
  delete_node_pool lennygvisor
fi

if has_class kata; then
  log "removing Kata SDK-warm pool + RuntimeClass + node pool"
  kubectl delete sandboxwarmpool load-preconnect-pool-kata -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-kata -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-kata --ignore-not-found
  kubectl delete runtimeclass kata --ignore-not-found
  delete_node_pool lennykata
fi

log "done"
