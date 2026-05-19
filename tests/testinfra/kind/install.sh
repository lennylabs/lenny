# SPDX-License-Identifier: MIT
#!/usr/bin/env bash
#
# install.sh stands up a full Lenny control plane on a local Kind
# cluster for the tier-5 e2e suite. It is the bring-up step the Go
# harness in install.go expects: the harness verifies the install and
# skips when it is absent; this script performs the install.
#
# The script is idempotent. Each step checks whether its result is
# already present and skips when so, so a re-run after a partial
# failure resumes rather than duplicates work. Re-running a completed
# install is a no-op beyond the verification reads.
#
# Steps:
#   1. Build the ten platform binary images, each as a separate
#      `docker build` process.
#   2. Load the images onto the Kind cluster nodes.
#   3. Create the Kind cluster from tests/testinfra/kind/cluster.yaml.
#   4. Create the lenny-system and monitoring namespaces.
#   5. Install cert-manager and wait for it to become Available.
#   6. Install the prometheus-operator CRDs the chart's monitoring
#      templates depend on.
#   7. Install the Lenny Helm chart with the e2e values overlay.
#
# Environment variables:
#   LENNY_KIND_CLUSTER   Cluster name. Default: lenny-e2e.
#   LENNY_IMAGE_TAG      Image tag for the built binaries. Default: e2e.
#   LENNY_SKIP_BUILD     When "1", skip the image build and load steps
#                        (the images are assumed already loaded).

set -euo pipefail

CLUSTER="${LENNY_KIND_CLUSTER:-lenny-e2e}"
TAG="${LENNY_IMAGE_TAG:-e2e}"
CERT_MANAGER_VERSION="v1.16.2"
PROM_OPERATOR_VERSION="v0.79.2"

# Resolve the repository root from this script's location so the
# script runs correctly regardless of the caller's working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

KIND_CONFIG="${REPO_ROOT}/tests/testinfra/kind/cluster.yaml"
E2E_VALUES="${REPO_ROOT}/tests/testinfra/kind/e2e-values.yaml"
CHART_DIR="${REPO_ROOT}/charts/lenny"

# The ten platform binaries. Each maps to a cmd/<binary> package and is
# built into a <binary>:<tag> image.
BINARIES=(
  lenny-controller
  lenny-gateway
  lenny-webhook
  lenny-token-service
  lenny-ops
  lenny-migrate
  lenny-adapter
  lenny-preflight
  lenny-backup
  lenny-ctl
)

log() { printf '==> %s\n' "$*"; }

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required tool '$1' is not on PATH" >&2
    exit 1
  fi
}

require docker
require kind
require kubectl
require helm

if ! docker info >/dev/null 2>&1; then
  echo "error: the Docker daemon is not reachable" >&2
  exit 1
fi

# ---------------------------------------------------------------------
# Step 1+2: build and load the binary images.
#
# Each image is built with its own `docker build` invocation. A shell
# `for` loop that redirects output does not land the image in this
# host's Docker Desktop daemon store, so every build runs as its own
# foreground process with output going to the terminal. If an image
# does not appear in `docker images` after a build, the build falls
# back to DOCKER_BUILDKIT=0, which writes to the legacy image store.
# ---------------------------------------------------------------------
build_image() {
  local binary="$1"
  local image="${binary}:${TAG}"

  if docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} already built; skipping"
    return 0
  fi

  log "building ${image}"
  docker build --build-arg "BINARY=${binary}" -t "${image}" "${REPO_ROOT}"

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} not found after build; retrying with DOCKER_BUILDKIT=0"
    DOCKER_BUILDKIT=0 docker build --build-arg "BINARY=${binary}" -t "${image}" "${REPO_ROOT}"
  fi

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    echo "error: image ${image} is still absent after both build attempts" >&2
    exit 1
  fi
}

if [[ "${LENNY_SKIP_BUILD:-}" == "1" ]]; then
  log "LENNY_SKIP_BUILD=1; skipping image build and load"
else
  for binary in "${BINARIES[@]}"; do
    build_image "${binary}"
  done
fi

# ---------------------------------------------------------------------
# Step 3: create the Kind cluster (idempotent).
# ---------------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  log "kind cluster ${CLUSTER} already exists; skipping create"
else
  log "creating kind cluster ${CLUSTER}"
  kind create cluster --name "${CLUSTER}" --config "${KIND_CONFIG}"
fi

# kubectl from here on targets the Kind cluster's context explicitly so
# the script does not depend on the caller's current-context.
KCTX="kind-${CLUSTER}"
kc() { kubectl --context "${KCTX}" "$@"; }

# ---------------------------------------------------------------------
# Step 2 (load): load the built images onto the cluster nodes. Done
# after cluster creation because `kind load` needs the cluster to exist.
# ---------------------------------------------------------------------
if [[ "${LENNY_SKIP_BUILD:-}" != "1" ]]; then
  images=()
  for binary in "${BINARIES[@]}"; do
    images+=("${binary}:${TAG}")
  done
  log "loading ${#images[@]} images onto cluster ${CLUSTER}"
  kind load docker-image --name "${CLUSTER}" "${images[@]}"
fi

# ---------------------------------------------------------------------
# Step 4: create the lenny-system and monitoring namespaces.
# ---------------------------------------------------------------------
for ns in lenny-system monitoring; do
  if kc get namespace "${ns}" >/dev/null 2>&1; then
    log "namespace ${ns} already exists; skipping"
  else
    log "creating namespace ${ns}"
    kc create namespace "${ns}"
  fi
done

# ---------------------------------------------------------------------
# Step 5: install cert-manager and wait for it to become Available.
# ---------------------------------------------------------------------
if kc get namespace cert-manager >/dev/null 2>&1 &&
  kc -n cert-manager get deploy cert-manager >/dev/null 2>&1; then
  log "cert-manager already installed; skipping apply"
else
  log "installing cert-manager ${CERT_MANAGER_VERSION}"
  kc apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
fi
log "waiting for cert-manager deployments to become Available"
kc -n cert-manager wait --for=condition=Available deploy --all --timeout=180s

# ---------------------------------------------------------------------
# Step 6: install the prometheus-operator CRDs the chart depends on.
# ---------------------------------------------------------------------
PROM_CRD_BASE="https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/${PROM_OPERATOR_VERSION}/example/prometheus-operator-crd"
for crd in prometheusrules servicemonitors podmonitors; do
  if kc get crd "${crd}.monitoring.coreos.com" >/dev/null 2>&1; then
    log "prometheus-operator CRD ${crd} already installed; skipping"
  else
    log "installing prometheus-operator CRD ${crd}"
    kc apply --server-side -f "${PROM_CRD_BASE}/monitoring.coreos.com_${crd}.yaml"
  fi
done

# ---------------------------------------------------------------------
# Step 7: install the Lenny Helm chart.
# ---------------------------------------------------------------------
if helm status lenny -n lenny-system --kube-context "${KCTX}" >/dev/null 2>&1; then
  log "Helm release lenny is already installed in lenny-system; skipping install"
else
  log "installing the Lenny Helm chart"
  helm install lenny "${CHART_DIR}" \
    -n lenny-system \
    --kube-context "${KCTX}" \
    -f "${E2E_VALUES}" \
    --timeout 420s
fi

log "waiting for the lenny-system control-plane pods to become Ready"
kc -n lenny-system wait --for=condition=Ready pod \
  -l app.kubernetes.io/name=lenny \
  --timeout=300s

log "Lenny is installed on the ${CLUSTER} Kind cluster."
log "Run the tier-5 e2e suite with:"
log "  go test -tags=e2e_kind -count=1 ./tests/tier5_e2e_kind/..."
