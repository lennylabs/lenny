#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/gcp/down-runtimeclass-pools.sh — tears down the gVisor and
# Kata node pools, RuntimeClasses, and SDK-warm pools that
# up-runtimeclass-pools.sh created for the §6.3 startup-latency benchmark.
# Safe to run with classes that were never provisioned: each delete
# tolerates a missing target.
#
# Usage:
#   GCP_PROJECT=acme-prod GCP_REGION=us-central1 \
#   LENNY_RELEASE=lenny-load-small \
#   LENNY_BENCH_RUNTIME_CLASSES=gvisor,kata \
#   scripts/cloud/gcp/down-runtimeclass-pools.sh

set -euo pipefail

PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"
REGION="${GCP_REGION:-us-central1}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${GKE_CLUSTER_NAME:-${RELEASE}-gke}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-gvisor,kata}"

for cli in gcloud kubectl; do
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

gcloud container clusters get-credentials "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" >/dev/null 2>&1 || true

delete_node_pool() {
  gcloud container node-pools delete "$1" --cluster "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" --quiet 2>/dev/null || true
}

if has_class gvisor; then
  log "removing gVisor SDK-warm pool + node pool"
  kubectl delete sandboxwarmpool load-preconnect-pool-gvisor -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-gvisor -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-gvisor --ignore-not-found
  delete_node_pool lenny-gvisor
fi

if has_class kata; then
  log "removing Kata SDK-warm pool + RuntimeClass + node pool"
  kubectl delete sandboxwarmpool load-preconnect-pool-kata -n lenny-agents --ignore-not-found
  kubectl delete sandboxtemplate load-preconnect-template-kata -n lenny-agents --ignore-not-found
  kubectl delete runtime load-preconnect-runtime-kata --ignore-not-found
  kubectl delete runtimeclass kata --ignore-not-found
  delete_node_pool lenny-kata
fi

log "done"
