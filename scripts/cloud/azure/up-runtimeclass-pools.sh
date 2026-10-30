#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/azure/up-runtimeclass-pools.sh — provisions the gVisor and
# Kata node pools, RuntimeClasses, and §6.1 SDK-warm pools the §6.3
# startup-latency benchmark's per-runtime-class arms need (tier 12,
# TestCloudStartupLatencySDKWarm{Gvisor,Kata}).
#
# Benchmark-only, opt-in helper, separate from up.sh (which treats the AKS
# cluster and its node pools as operator-supplied). Run after up.sh +
# install-lenny.sh, before run-load.sh.
#
# AKS has no managed gVisor, so the gVisor node pool needs runsc installed
# on its nodes. Kata is available through AKS Pod Sandboxing on a
# nested-virt-capable VM size; this script labels and taints a Kata node
# pool and applies the kata RuntimeClass, and the operator enables Pod
# Sandboxing / installs kata-containers on those nodes.
#
# Usage:
#   AZURE_RESOURCE_GROUP=lenny-load AKS_CLUSTER_NAME=lenny-load-small-aks \
#   LENNY_BENCH_RUNTIME_CLASSES=runc,gvisor,kata \
#   LOAD_PRECONNECT_RUNTIME_IMAGE=<acr>/lenny-runtime-preconnect-echo:<tag> \
#   scripts/cloud/azure/up-runtimeclass-pools.sh

set -euo pipefail

RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${AKS_CLUSTER_NAME:-${RELEASE}-aks}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-runc}"
NODE_VM_SIZE="${BENCH_NODE_VM_SIZE:-Standard_D4s_v5}"
KATA_VM_SIZE="${BENCH_KATA_VM_SIZE:-Standard_D4s_v5}"
NODE_COUNT="${BENCH_NODE_COUNT:-1}"

export LOAD_PRECONNECT_RUNTIME_IMAGE="${LOAD_PRECONNECT_RUNTIME_IMAGE:?LOAD_PRECONNECT_RUNTIME_IMAGE must be set to the pushed preconnect-echo image}"
export LOAD_SESSION_MIN="${LOAD_SESSION_MIN:-2}"
export LOAD_SESSION_MAX="${LOAD_SESSION_MAX:-4}"

for cli in az kubectl envsubst; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "up-runtimeclass-pools.sh: ${cli} not on PATH" >&2; exit 3; }
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
K8S_DIR="${REPO_ROOT}/tests/testinfra/k8s"

log() { printf '==> %s\n' "$*" >&2; }

has_class() {
  local want="$1"
  local IFS=','
  for c in ${CLASSES}; do
    [[ "${c// /}" == "${want}" ]] && return 0
  done
  return 1
}

node_pool_exists() {
  az aks nodepool show --resource-group "${RESOURCE_GROUP}" --cluster-name "${CLUSTER}" --name "$1" >/dev/null 2>&1
}

# Point kubectl at the target AKS cluster before any apply, so a standalone
# invocation does not act against whatever context happens to be current.
az aks get-credentials --resource-group "${RESOURCE_GROUP}" --name "${CLUSTER}" --overwrite-existing >/dev/null

if has_class gvisor; then
  log "gVisor: AKS node pool 'lennygvisor' (runsc must be installed on the nodes)"
  if node_pool_exists lennygvisor; then
    log "  node pool already exists, reusing"
  else
    az aks nodepool add --resource-group "${RESOURCE_GROUP}" --cluster-name "${CLUSTER}" \
      --name lennygvisor --node-vm-size "${NODE_VM_SIZE}" --node-count "${NODE_COUNT}" \
      --labels "lenny.dev/runtime-pool=gvisor"
  fi
  log "  applying gVisor RuntimeClass + SDK-warm pool"
  export GVISOR_HANDLER="${GVISOR_HANDLER:-runsc}"
  export GVISOR_NODE_LABEL_KEY="lenny.dev/runtime-pool"
  export GVISOR_NODE_LABEL_VALUE="gvisor"
  envsubst < "${K8S_DIR}/runtimeclass-gvisor.yaml.tmpl" | kubectl apply -f -
  envsubst < "${K8S_DIR}/runtimeclass-pool-gvisor.yaml.tmpl" | kubectl apply -f -
fi

if has_class kata; then
  log "Kata: AKS node pool 'lennykata' (Pod Sandboxing / kata-containers on a nested-virt VM size)"
  if node_pool_exists lennykata; then
    log "  node pool already exists, reusing"
  else
    az aks nodepool add --resource-group "${RESOURCE_GROUP}" --cluster-name "${CLUSTER}" \
      --name lennykata --node-vm-size "${KATA_VM_SIZE}" --node-count "${NODE_COUNT}" \
      --labels "lenny.dev/node-pool=kata" \
      --node-taints "lenny.dev/isolation=kata:NoSchedule"
  fi
  log "  applying kata RuntimeClass + SDK-warm pool"
  export KATA_HANDLER="${KATA_HANDLER:-kata-qemu}"
  export KATA_NODE_LABEL_KEY="lenny.dev/node-pool"
  export KATA_NODE_LABEL_VALUE="kata"
  envsubst < "${K8S_DIR}/runtimeclass-kata.yaml.tmpl" | kubectl apply -f -
  envsubst < "${K8S_DIR}/runtimeclass-pool-kata.yaml.tmpl" | kubectl apply -f -
  log "  NOTE: enable AKS Pod Sandboxing / install kata-containers on the"
  log "  lennykata nodes so the '${KATA_HANDLER}' handler resolves; until then"
  log "  the pool stays Degraded."
fi

log "done. Set LENNY_BENCH_RUNTIME_CLASSES=${CLASSES} for the tier-12 run."
