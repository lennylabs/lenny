#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# install-gvisor.sh installs the gVisor (runsc) container runtime onto
# the Kind node(s) cluster.yaml labels lenny.dev/pool=sandbox-gvisor,
# and applies the §5.3 `gvisor` RuntimeClass so a pool with
# `isolationProfile: sandboxed` schedules a real gVisor-sandboxed pod
# instead of a vacuous skip. install.sh calls this script right after
# cluster creation (before any workload exists on the node), so the
# containerd restart this script performs does not disturb a running
# agent pod.
#
# gVisor needs no nested virtualization (its ptrace/systrap platform
# runs inside an unprivileged-enough container), so it installs cleanly
# on a Kind node, which is itself a Docker container. This script:
#   1. Finds the node(s) carrying the sandbox-gvisor label.
#   2. Downloads the runsc + containerd-shim-runsc-v1 release binaries
#      matching the node's architecture (cached on the host so a re-run
#      or a second node on the same arch does not re-download ~150MB).
#   3. Copies them onto the node and registers the `runsc` containerd
#      runtime handler, then restarts containerd.
#   4. Applies the `gvisor` RuntimeClass (spec: §5.3), scoped by
#      scheduling.nodeSelector to the labelled node(s).
#
# Idempotent: a node that already has the pinned runsc version installed
# skips steps 2-3 (and therefore never restarts containerd on a re-run of
# an already-provisioned node).
#
# Environment variables:
#   LENNY_KIND_SKIP_GVISOR   When "1", this script is a no-op. Escape
#                            hatch for a host with no route to
#                            storage.googleapis.com (the gVisor release
#                            bucket); the gvisor_isolation_test.go tier-5
#                            test gates on RuntimeClass presence and
#                            skips cleanly when this is set.
#   LENNY_GVISOR_VERSION     gVisor release channel/version directory
#                            under the release bucket. Default: latest.

set -euo pipefail

if [[ "${LENNY_KIND_SKIP_GVISOR:-}" == "1" ]]; then
  echo "==> install-gvisor.sh: LENNY_KIND_SKIP_GVISOR=1; skipping gVisor install" >&2
  exit 0
fi

CLUSTER="${LENNY_KIND_CLUSTER:-lenny-e2e}"
GVISOR_VERSION="${LENNY_GVISOR_VERSION:-latest}"
NODE_LABEL_KEY="lenny.dev/pool"
NODE_LABEL_VALUE="sandbox-gvisor"
GVISOR_HANDLER="runsc"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
K8S_DIR="${REPO_ROOT}/tests/testinfra/k8s"
CACHE_DIR="${TMPDIR:-/tmp}/lenny-gvisor-cache/${GVISOR_VERSION}"

log() { printf '==> install-gvisor.sh: %s\n' "$*" >&2; }

KCTX="kind-${CLUSTER}"
kc() { kubectl --context "${KCTX}" "$@"; }

# A Kind node's Kubernetes object name equals its Docker container
# name, so the k8s node label lookup below addresses the container
# directly with no extra name translation.
nodes="$(kind get nodes --name "${CLUSTER}" 2>/dev/null | grep -v '\-control-plane$' || true)"
gvisor_nodes=""
for node in ${nodes}; do
  has_label="$(kc get node "${node}" -o jsonpath="{.metadata.labels['${NODE_LABEL_KEY//./\\.}']}" 2>/dev/null || true)"
  if [[ "${has_label}" == "${NODE_LABEL_VALUE}" ]]; then
    gvisor_nodes="${gvisor_nodes} ${node}"
  fi
done

if [[ -z "${gvisor_nodes// /}" ]]; then
  log "no node labeled ${NODE_LABEL_KEY}=${NODE_LABEL_VALUE} on cluster ${CLUSTER}; skipping gVisor install"
  log "(tests/testinfra/kind/cluster.yaml labels the second worker; a custom LENNY_CLUSTER_CONFIG may omit it)"
  exit 0
fi

# download_gvisor_binary <arch> <binary-name> caches the release binary
# for <arch> under CACHE_DIR and echoes its local path. Idempotent: a
# cached file is reused across nodes and re-runs.
download_gvisor_binary() {
  local arch="$1" name="$2"
  local dest="${CACHE_DIR}/${arch}/${name}"
  if [[ -s "${dest}" ]]; then
    echo "${dest}"
    return 0
  fi
  mkdir -p "$(dirname "${dest}")"
  local url="https://storage.googleapis.com/gvisor/releases/release/${GVISOR_VERSION}/${arch}/${name}"
  log "downloading ${url}"
  curl -fSL -m 300 -o "${dest}.tmp" "${url}"
  chmod +x "${dest}.tmp"
  mv "${dest}.tmp" "${dest}"
  echo "${dest}"
}

# gvisorArch maps a node's `uname -m` to the gVisor release bucket's
# per-architecture directory name.
gvisor_arch() {
  case "$1" in
    x86_64) echo "x86_64" ;;
    aarch64 | arm64) echo "aarch64" ;;
    *)
      echo "install-gvisor.sh: unsupported node architecture '$1'" >&2
      return 1
      ;;
  esac
}

for node in ${gvisor_nodes}; do
  node_arch="$(docker exec "${node}" uname -m)"
  bucket_arch="$(gvisor_arch "${node_arch}")"

  installed_version=""
  if docker exec "${node}" test -x /usr/local/bin/runsc 2>/dev/null; then
    installed_version="$(docker exec "${node}" /usr/local/bin/runsc --version 2>/dev/null | head -1 || true)"
  fi
  if [[ -n "${installed_version}" ]]; then
    log "${node}: runsc already installed (${installed_version}); skipping binary install and containerd restart"
  else
    log "${node}: installing gVisor (${bucket_arch}, ${GVISOR_VERSION})"
    runsc_bin="$(download_gvisor_binary "${bucket_arch}" runsc)"
    shim_bin="$(download_gvisor_binary "${bucket_arch}" containerd-shim-runsc-v1)"
    docker cp "${runsc_bin}" "${node}:/usr/local/bin/runsc"
    docker cp "${shim_bin}" "${node}:/usr/local/bin/containerd-shim-runsc-v1"
    docker exec "${node}" chmod +x /usr/local/bin/runsc /usr/local/bin/containerd-shim-runsc-v1

    # Register the runsc runtime handler with containerd, then restart
    # so the new [plugins...runtimes.runsc] block takes effect. Guard the
    # config append so a re-run (installed_version empty only because a
    # prior run was interrupted after the binary copy but before this
    # point) does not duplicate the block.
    if ! docker exec "${node}" grep -q 'runtimes\.runsc' /etc/containerd/config.toml; then
      docker exec "${node}" sh -c 'cat >> /etc/containerd/config.toml <<EOF

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF'
    fi
    log "${node}: restarting containerd to pick up the runsc runtime handler"
    docker exec "${node}" systemctl restart containerd
    for _ in $(seq 1 30); do
      docker exec "${node}" systemctl is-active containerd >/dev/null 2>&1 && break
      sleep 1
    done
    if ! docker exec "${node}" systemctl is-active containerd >/dev/null 2>&1; then
      echo "install-gvisor.sh: containerd did not become active on ${node} after restart" >&2
      exit 1
    fi
  fi
done

log "applying the gvisor RuntimeClass (handler=${GVISOR_HANDLER}, nodeSelector ${NODE_LABEL_KEY}=${NODE_LABEL_VALUE})"
GVISOR_HANDLER="${GVISOR_HANDLER}" \
GVISOR_NODE_LABEL_KEY="${NODE_LABEL_KEY}" \
GVISOR_NODE_LABEL_VALUE="${NODE_LABEL_VALUE}" \
  envsubst <"${K8S_DIR}/runtimeclass-gvisor.yaml.tmpl" | kc apply -f -

log "gVisor install complete on:${gvisor_nodes}"
