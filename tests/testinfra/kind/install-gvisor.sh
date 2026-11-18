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
#   2. Downloads the release archive gvisor.tar.bz2 for the node's
#      architecture and its published gvisor.tar.bz2.sha512, verifies the
#      archive against that checksum, and extracts it. The archive holds
#      runsc, containerd-shim-runsc-v1, and the gvisor-bin/ directory of
#      helper binaries (the Sentry among them) that runsc looks up next
#      to its own binary. The verified archive and its extracted tree are
#      cached on the host per version and architecture, so a re-run or a
#      second node on the same architecture does not download the ~80MB
#      archive again. A missing checksum file or a mismatch stops the
#      install.
#   3. Copies runsc, containerd-shim-runsc-v1, and gvisor-bin/ into the
#      node's /usr/local/bin, registers the `runsc` containerd runtime
#      handler, and restarts containerd.
#   4. Applies the `gvisor` RuntimeClass (spec: §5.3), scoped by
#      scheduling.nodeSelector to the labelled node(s).
#
# The release bucket publishes each release as gvisor.tar.bz2 and
# gvisor.tar.zstd. This script uses the bzip2 archive because bzip2 ships
# with macOS and with the Debian-based gate image, and tar reads it with
# -j on both, while zstd is a separate install on macOS.
#
# Idempotent: the install writes the release it installed to
# /etc/lenny-gvisor-release on the node, and a node whose marker names
# the requested release and whose runsc runs skips steps 2-3 (and
# therefore never restarts containerd on a re-run of an
# already-provisioned node). A node with any other runsc, or none, gets
# the requested release.
#
# Environment variables:
#   LENNY_KIND_SKIP_GVISOR   When "1", this script is a no-op. Escape
#                            hatch for a host with no route to
#                            storage.googleapis.com (the gVisor release
#                            bucket); the gvisor_isolation_test.go the e2e_kind tier
#                            test gates on RuntimeClass presence and
#                            skips cleanly when this is set.
#   LENNY_GVISOR_VERSION     gVisor release directory under the release
#                            bucket, such as 20261116. Default: the
#                            release GVISOR_DEFAULT_VERSION pins below.
#                            "latest" names the newest release, which
#                            the host cache keeps under that name until
#                            the cache directory is removed.

set -euo pipefail

if [[ "${LENNY_KIND_SKIP_GVISOR:-}" == "1" ]]; then
  echo "==> install-gvisor.sh: LENNY_KIND_SKIP_GVISOR=1; skipping gVisor install" >&2
  exit 0
fi

CLUSTER="${LENNY_KIND_CLUSTER:-lenny-e2e}"
# GVISOR_DEFAULT_VERSION is the newest release directory the release
# bucket listed when this pin was last moved. To move it, list the
# release directories with
#   curl -s 'https://storage.googleapis.com/storage/v1/b/gvisor/o?prefix=releases/release/&delimiter=/'
# following nextPageToken, and pick the newest dated directory that
# carries gvisor.tar.bz2 and gvisor.tar.bz2.sha512 for both x86_64 and
# aarch64.
GVISOR_DEFAULT_VERSION="20261116"
GVISOR_VERSION="${LENNY_GVISOR_VERSION:-${GVISOR_DEFAULT_VERSION}}"
NODE_LABEL_KEY="lenny.dev/pool"
NODE_LABEL_VALUE="sandbox-gvisor"
GVISOR_HANDLER="runsc"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
K8S_DIR="${REPO_ROOT}/tests/testinfra/k8s"
CACHE_DIR="${TMPDIR:-/tmp}/lenny-gvisor-cache/${GVISOR_VERSION}"
NODE_MARKER="/etc/lenny-gvisor-release"

log() { printf '==> install-gvisor.sh: %s\n' "$*" >&2; }

# begin gvisor_fetch
GVISOR_RELEASE_URL="https://storage.googleapis.com/gvisor/releases/release"
GVISOR_ARCHIVE="gvisor.tar.bz2"

# sha512_of <file> prints the hex SHA-512 digest of <file>. Linux and
# recent macOS carry sha512sum; older macOS carries only shasum.
sha512_of() {
  if command -v sha512sum >/dev/null 2>&1; then
    sha512sum "$1" | awk '{print $1}'
  else
    shasum -a 512 "$1" | awk '{print $1}'
  fi
}

# verify_gvisor_archive <archive> <checksum-file> succeeds only when the
# checksum file's first field is a SHA-512 hex digest equal to the
# archive's digest.
verify_gvisor_archive() {
  local archive="$1" sumfile="$2" want got
  if [[ ! -s "${sumfile}" ]]; then
    echo "install-gvisor.sh: checksum file ${sumfile} is missing or empty" >&2
    return 1
  fi
  want="$(awk 'NR == 1 {print tolower($1)}' "${sumfile}")"
  if [[ ! "${want}" =~ ^[0-9a-f]{128}$ ]]; then
    echo "install-gvisor.sh: ${sumfile} does not hold a SHA-512 digest" >&2
    return 1
  fi
  got="$(sha512_of "${archive}")" || return 1
  if [[ "${got}" != "${want}" ]]; then
    echo "install-gvisor.sh: SHA-512 mismatch for ${archive}: published ${want}, downloaded ${got}" >&2
    return 1
  fi
}

# gvisor_tree_complete <dir> succeeds when <dir> holds the archive
# members the node install copies.
gvisor_tree_complete() {
  [[ -x "$1/runsc" && -x "$1/containerd-shim-runsc-v1" && -d "$1/gvisor-bin" ]]
}

# fetch_gvisor_release <arch> downloads, verifies, and extracts the
# release archive for <arch> under CACHE_DIR, and echoes the directory
# holding the extracted runsc, containerd-shim-runsc-v1, and gvisor-bin/.
# A cached archive is verified again against its cached checksum file
# before its tree is reused, and a cache entry that fails is discarded
# and fetched again. The call fails on a failed download, a missing
# checksum file, a checksum mismatch, an archive member with an absolute
# path or a ".." component, or an archive without the expected members.
fetch_gvisor_release() {
  local arch="$1"
  local dir="${CACHE_DIR}/${arch}"
  local archive="${dir}/${GVISOR_ARCHIVE}"
  local sumfile="${archive}.sha512"
  local tree="${dir}/tree"
  local url="${GVISOR_RELEASE_URL}/${GVISOR_VERSION}/${arch}/${GVISOR_ARCHIVE}"

  if [[ -s "${archive}" ]] && gvisor_tree_complete "${tree}" &&
    verify_gvisor_archive "${archive}" "${sumfile}" 2>/dev/null; then
    echo "${tree}"
    return 0
  fi

  rm -rf "${dir}"
  mkdir -p "${dir}" || return 1
  log "downloading ${url}.sha512"
  if ! curl -fsSL -m 60 -o "${sumfile}" "${url}.sha512"; then
    echo "install-gvisor.sh: cannot download the published checksum ${url}.sha512; refusing to install an unverified archive" >&2
    rm -f "${sumfile}"
    return 1
  fi
  log "downloading ${url}"
  if ! curl -fSL -m 600 -o "${archive}.tmp" "${url}"; then
    echo "install-gvisor.sh: cannot download ${url}" >&2
    rm -f "${archive}.tmp"
    return 1
  fi
  if ! verify_gvisor_archive "${archive}.tmp" "${sumfile}"; then
    rm -f "${archive}.tmp" "${sumfile}"
    return 1
  fi
  mv "${archive}.tmp" "${archive}" || return 1

  local members
  members="$(tar -tjf "${archive}")" || return 1
  if printf '%s\n' "${members}" | grep -Eq '^/|(^|/)\.\.(/|$)'; then
    echo "install-gvisor.sh: ${archive} has a member outside its root; refusing to extract it" >&2
    return 1
  fi
  rm -rf "${tree}.tmp"
  mkdir -p "${tree}.tmp" || return 1
  tar -xjf "${archive}" -C "${tree}.tmp" || return 1
  if ! gvisor_tree_complete "${tree}.tmp"; then
    echo "install-gvisor.sh: ${archive} lacks runsc, containerd-shim-runsc-v1, or gvisor-bin/" >&2
    return 1
  fi
  mv "${tree}.tmp" "${tree}" || return 1
  echo "${tree}"
}
# end gvisor_fetch

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

  installed_release="$(docker exec "${node}" cat "${NODE_MARKER}" 2>/dev/null || true)"
  installed_version=""
  if docker exec "${node}" test -x /usr/local/bin/runsc 2>/dev/null; then
    installed_version="$(docker exec "${node}" /usr/local/bin/runsc --version 2>/dev/null | head -1 || true)"
  fi
  if [[ "${installed_release}" == "${GVISOR_VERSION}" && -n "${installed_version}" ]]; then
    log "${node}: gVisor ${GVISOR_VERSION} already installed (${installed_version}); skipping binary install and containerd restart"
  else
    log "${node}: installing gVisor (${bucket_arch}, ${GVISOR_VERSION})"
    gvisor_tree="$(fetch_gvisor_release "${bucket_arch}")" || exit 1
    # Replace the whole gvisor-bin/ directory so no helper of an earlier
    # release stays beside the new runsc. runsc looks gvisor-bin/ up
    # next to its own binary, so both sit in /usr/local/bin.
    docker exec "${node}" rm -rf /usr/local/bin/gvisor-bin
    docker cp "${gvisor_tree}/runsc" "${node}:/usr/local/bin/runsc"
    docker cp "${gvisor_tree}/containerd-shim-runsc-v1" "${node}:/usr/local/bin/containerd-shim-runsc-v1"
    docker cp "${gvisor_tree}/gvisor-bin" "${node}:/usr/local/bin/gvisor-bin"
    docker exec "${node}" chown -R root:root /usr/local/bin/runsc /usr/local/bin/containerd-shim-runsc-v1 /usr/local/bin/gvisor-bin
    docker exec "${node}" chmod -R a+rX /usr/local/bin/gvisor-bin
    docker exec "${node}" chmod 0755 /usr/local/bin/runsc /usr/local/bin/containerd-shim-runsc-v1

    # Register the runsc runtime handler with containerd, then restart
    # so the new [plugins...runtimes.runsc] block takes effect. Guard the
    # config append so a re-run (the marker absent because a prior run
    # was interrupted after the config append, or an earlier release
    # installed) does not duplicate the block.
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
    installed_version="$(docker exec "${node}" /usr/local/bin/runsc --version 2>/dev/null | head -1 || true)"
    if [[ -z "${installed_version}" ]]; then
      echo "install-gvisor.sh: /usr/local/bin/runsc on ${node} does not run after the install" >&2
      exit 1
    fi
    # The marker is written last, so an install interrupted before this
    # point runs again in full on the next invocation.
    printf '%s\n' "${GVISOR_VERSION}" | docker exec -i "${node}" sh -c "cat > ${NODE_MARKER}"
    log "${node}: installed ${installed_version}"
  fi
done

log "applying the gvisor RuntimeClass (handler=${GVISOR_HANDLER}, nodeSelector ${NODE_LABEL_KEY}=${NODE_LABEL_VALUE})"
GVISOR_HANDLER="${GVISOR_HANDLER}" \
GVISOR_NODE_LABEL_KEY="${NODE_LABEL_KEY}" \
GVISOR_NODE_LABEL_VALUE="${NODE_LABEL_VALUE}" \
  envsubst <"${K8S_DIR}/runtimeclass-gvisor.yaml.tmpl" | kc apply -f -

log "gVisor install complete on:${gvisor_nodes}"
