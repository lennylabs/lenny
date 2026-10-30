#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/aws/down-runtimeclass-pools.sh — tears down the gVisor and
# Kata node groups, RuntimeClasses, and SDK-warm pools that
# up-runtimeclass-pools.sh created. Tolerates missing targets.
#
# Usage:
#   AWS_REGION=us-east-1 LENNY_RELEASE=lenny-load-small \
#   EKS_CLUSTER_NAME=lenny-load-small-eks \
#   LENNY_BENCH_RUNTIME_CLASSES=gvisor,kata \
#   scripts/cloud/aws/down-runtimeclass-pools.sh

set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${EKS_CLUSTER_NAME:-${RELEASE}-eks}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-gvisor,kata}"

for cli in eksctl kubectl; do
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

delete_node_group() {
  eksctl delete nodegroup --cluster "${CLUSTER}" --region "${REGION}" --name "$1" --disable-eviction 2>/dev/null || true
}

if has_class gvisor; then
  log "removing gVisor SDK-warm pool + RuntimeClass + node group"
  kubectl delete sandboxwarmpool load-preconnect-pool-gvisor -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-gvisor -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-gvisor --ignore-not-found
  kubectl delete runtimeclass gvisor --ignore-not-found
  delete_node_group lenny-gvisor
fi

if has_class kata; then
  log "removing Kata SDK-warm pool + RuntimeClass + node group"
  kubectl delete sandboxwarmpool load-preconnect-pool-kata -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-kata -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-kata --ignore-not-found
  kubectl delete runtimeclass kata --ignore-not-found
  delete_node_group lenny-kata
fi

log "done"
