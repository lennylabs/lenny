#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/cloud/aws/up-runtimeclass-pools.sh — provisions the gVisor and
# Kata node groups, RuntimeClasses, and §6.1 SDK-warm pools the §6.3
# startup-latency benchmark's per-runtime-class arms need (tier 12,
# TestCloudStartupLatencySDKWarm{Gvisor,Kata}).
#
# Benchmark-only, opt-in helper, deliberately separate from up.sh (which
# treats the EKS cluster and its node groups as operator-supplied). Run
# after up.sh + install-lenny.sh, before run-load.sh.
#
# EKS has no managed gVisor or Kata runtime, so each node group needs the
# runtime binary installed on its nodes:
#   - gVisor: a node AMI or bootstrap that installs runsc and registers it
#     as a containerd handler. Any Linux node hosts it (no nested virt).
#   - Kata: a bare-metal (*.metal) instance type that exposes /dev/kvm,
#     with kata-containers installed (for example the kata-deploy
#     DaemonSet). Nested virtualization is not available on shared EC2
#     instance types, so the Kata node group MUST be metal.
# Set BENCH_GVISOR_AMI / node bootstrap out of band; this script creates
# the node groups, labels/taints them, and applies the RuntimeClasses and
# pools. It does not bake AMIs.
#
# Usage:
#   AWS_REGION=us-east-1 LENNY_RELEASE=lenny-load-small \
#   EKS_CLUSTER_NAME=lenny-load-small-eks \
#   LENNY_BENCH_RUNTIME_CLASSES=runc,gvisor,kata \
#   LOAD_PRECONNECT_RUNTIME_IMAGE=<ecr>/lenny-runtime-preconnect-echo:<tag> \
#   scripts/cloud/aws/up-runtimeclass-pools.sh

set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
RELEASE="${LENNY_RELEASE:-lenny-load-small}"
CLUSTER="${EKS_CLUSTER_NAME:-${RELEASE}-eks}"
CLASSES="${LENNY_BENCH_RUNTIME_CLASSES:-runc}"
NODE_INSTANCE_TYPE="${BENCH_NODE_INSTANCE_TYPE:-m5.xlarge}"
KATA_INSTANCE_TYPE="${BENCH_KATA_INSTANCE_TYPE:-m5.metal}"
NODE_COUNT="${BENCH_NODE_COUNT:-1}"

export LOAD_PRECONNECT_RUNTIME_IMAGE="${LOAD_PRECONNECT_RUNTIME_IMAGE:?LOAD_PRECONNECT_RUNTIME_IMAGE must be set to the pushed preconnect-echo image}"
export LOAD_SESSION_MIN="${LOAD_SESSION_MIN:-2}"
export LOAD_SESSION_MAX="${LOAD_SESSION_MAX:-4}"

for cli in eksctl kubectl envsubst; do
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

node_group_exists() {
  eksctl get nodegroup --cluster "${CLUSTER}" --region "${REGION}" --name "$1" >/dev/null 2>&1
}

# Point kubectl at the target EKS cluster before any apply, so a standalone
# invocation does not act against whatever context happens to be current.
aws eks update-kubeconfig --region "${REGION}" --name "${CLUSTER}" >/dev/null

if has_class gvisor; then
  log "gVisor: EKS node group 'lenny-gvisor' (runsc must be installed on the node AMI)"
  if node_group_exists lenny-gvisor; then
    log "  node group already exists, reusing"
  else
    eksctl create nodegroup --cluster "${CLUSTER}" --region "${REGION}" \
      --name lenny-gvisor --node-type "${NODE_INSTANCE_TYPE}" --nodes "${NODE_COUNT}" \
      --node-labels "lenny.dev/runtime-pool=gvisor"
  fi
  log "  applying gVisor RuntimeClass + SDK-warm pool"
  export GVISOR_HANDLER="${GVISOR_HANDLER:-runsc}"
  export GVISOR_NODE_LABEL_KEY="lenny.dev/runtime-pool"
  export GVISOR_NODE_LABEL_VALUE="gvisor"
  envsubst < "${K8S_DIR}/runtimeclass-gvisor.yaml.tmpl" | kubectl apply -f -
  envsubst < "${K8S_DIR}/runtimeclass-pool-gvisor.yaml.tmpl" | kubectl apply -f -
fi

if has_class kata; then
  log "Kata: EKS bare-metal node group 'lenny-kata' (${KATA_INSTANCE_TYPE}; kata-containers must be installed)"
  if node_group_exists lenny-kata; then
    log "  node group already exists, reusing"
  else
    eksctl create nodegroup --cluster "${CLUSTER}" --region "${REGION}" \
      --name lenny-kata --node-type "${KATA_INSTANCE_TYPE}" --nodes "${NODE_COUNT}" \
      --node-labels "lenny.dev/node-pool=kata"
  fi
  # eksctl does not taint nodes, so apply the §17.2 dedicated-node taint
  # here on both the create and reuse paths (idempotent with --overwrite).
  kubectl taint nodes -l "lenny.dev/node-pool=kata" "lenny.dev/isolation=kata:NoSchedule" --overwrite
  log "  applying kata RuntimeClass + SDK-warm pool"
  export KATA_HANDLER="${KATA_HANDLER:-kata-qemu}"
  export KATA_NODE_LABEL_KEY="lenny.dev/node-pool"
  export KATA_NODE_LABEL_VALUE="kata"
  envsubst < "${K8S_DIR}/runtimeclass-kata.yaml.tmpl" | kubectl apply -f -
  envsubst < "${K8S_DIR}/runtimeclass-pool-kata.yaml.tmpl" | kubectl apply -f -
  log "  NOTE: install kata-containers on the lenny-kata nodes (kata-deploy)"
  log "  so the '${KATA_HANDLER}' handler resolves; until then the pool stays Degraded."
fi

log "done. Set LENNY_BENCH_RUNTIME_CLASSES=${CLASSES} for the tier-12 run."
