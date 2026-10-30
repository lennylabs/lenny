#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/gcp/up-runtimeclass-pools.sh — provisions the gVisor and
# Kata node pools, RuntimeClasses, and §6.1 SDK-warm pools the §6.3
# startup-latency benchmark's per-runtime-class arms need (tier 12,
# TestCloudStartupLatencySDKWarm{Gvisor,Kata}).
#
# This is a benchmark-only, opt-in helper. It is deliberately separate
# from up.sh, which provisions the durable per-release resources and
# treats the cluster and its node pools as operator-supplied. Run it after
# up.sh + install-lenny.sh, before run-load.sh, when benchmarking gVisor
# or Kata.
#
# gVisor uses GKE Sandbox (a --sandbox type=gvisor node pool), which
# provisions its own "gvisor" RuntimeClass and needs no hardware
# virtualization. Kata needs nested virtualization: GKE does not offer a
# managed Kata runtime, so the Kata path provisions a labelled, tainted
# nested-virt node pool and applies the kata RuntimeClass, and the
# operator must install kata-containers on those nodes (for example via
# the upstream kata-deploy DaemonSet) before the Kata pool warms.
#
# Usage:
#   GCP_PROJECT=acme-prod GCP_REGION=us-central1 \
#   LENNY_RELEASE=lenny-load-small \
#   LENNY_BENCH_RUNTIME_CLASSES=runc,gvisor,kata \
#   LOAD_PRECONNECT_RUNTIME_IMAGE=<registry>/lenny-runtime-preconnect-echo:<tag> \
#   scripts/cloud/gcp/up-runtimeclass-pools.sh

set -euo pipefail

PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"
REGION="${GCP_REGION:-us-central1}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${GKE_CLUSTER_NAME:-${RELEASE}-gke}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-runc}"
NODE_MACHINE_TYPE="${BENCH_NODE_MACHINE_TYPE:-n1-standard-4}"
NODE_COUNT="${BENCH_NODE_COUNT:-1}"

# Pool sizing + image the rendered templates consume.
export LOAD_PRECONNECT_RUNTIME_IMAGE="${LOAD_PRECONNECT_RUNTIME_IMAGE:?LOAD_PRECONNECT_RUNTIME_IMAGE must be set to the pushed preconnect-echo image}"
export LOAD_SESSION_MIN="${LOAD_SESSION_MIN:-2}"
export LOAD_SESSION_MAX="${LOAD_SESSION_MAX:-4}"

for cli in gcloud kubectl envsubst; do
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

gcloud container clusters get-credentials "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" >/dev/null

node_pool_exists() {
  gcloud container node-pools describe "$1" --cluster "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" >/dev/null 2>&1
}

if has_class gvisor; then
  log "gVisor: GKE Sandbox node pool 'lenny-gvisor'"
  if node_pool_exists lenny-gvisor; then
    log "  node pool already exists, reusing"
  else
    # The lenny.dev/runtime-pool label is informational on GKE: GKE Sandbox
    # owns the gvisor RuntimeClass and pins gvisor pods to the sandbox node
    # pool itself, so no nodeSelector keys off this label here.
    gcloud container node-pools create lenny-gvisor \
      --cluster "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" \
      --sandbox type=gvisor \
      --machine-type "${NODE_MACHINE_TYPE}" --num-nodes "${NODE_COUNT}" \
      --node-labels "lenny.dev/runtime-pool=gvisor"
  fi
  # GKE Sandbox owns the "gvisor" RuntimeClass; apply only the SDK-warm pool.
  log "  applying gVisor SDK-warm pool"
  envsubst < "${K8S_DIR}/runtimeclass-pool-gvisor.yaml.tmpl" | kubectl apply -f -
fi

if has_class kata; then
  log "Kata: nested-virt node pool 'lenny-kata'"
  if node_pool_exists lenny-kata; then
    log "  node pool already exists, reusing"
  else
    # Nested virtualization requires an Intel Haswell+ platform and the
    # enable-nested-virtualization node attribute.
    gcloud container node-pools create lenny-kata \
      --cluster "${CLUSTER}" --region "${REGION}" --project "${PROJECT}" \
      --machine-type "${BENCH_KATA_MACHINE_TYPE:-n1-standard-4}" --num-nodes "${NODE_COUNT}" \
      --min-cpu-platform "Intel Haswell" \
      --node-labels "lenny.dev/node-pool=kata" \
      --node-taints "lenny.dev/isolation=kata:NoSchedule"
  fi
  log "  applying kata RuntimeClass + SDK-warm pool"
  export KATA_HANDLER="${KATA_HANDLER:-kata-qemu}"
  export KATA_NODE_LABEL_KEY="lenny.dev/node-pool"
  export KATA_NODE_LABEL_VALUE="kata"
  envsubst < "${K8S_DIR}/runtimeclass-kata.yaml.tmpl" | kubectl apply -f -
  envsubst < "${K8S_DIR}/runtimeclass-pool-kata.yaml.tmpl" | kubectl apply -f -
  log "  WARNING: GKE has no managed Kata runtime. Install kata-containers"
  log "  on the lenny-kata nodes (for example the upstream kata-deploy"
  log "  DaemonSet) so the '${KATA_HANDLER}' handler resolves; until then the"
  log "  Kata pool stays Degraded and TestCloudStartupLatencySDKWarmKata"
  log "  records no observations."
fi

log "done. Set LENNY_BENCH_RUNTIME_CLASSES=${CLASSES} for the tier-12 run."
