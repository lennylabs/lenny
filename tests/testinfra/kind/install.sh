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
#   1. Build the platform binary images and the two reference echo
#      runtime images, each as a separate `docker build` process.
#   2. Load the images onto the Kind cluster nodes.
#   3. Create the Kind cluster from tests/testinfra/kind/cluster.yaml.
#   4. Create the lenny-system and monitoring namespaces.
#   5. Install cert-manager and wait for it to become Available.
#   6. Install the prometheus-operator CRDs the chart's monitoring
#      templates depend on.
#   7. Install ingress-nginx and wait for its controller to become
#      Available. The tier-5 gateway-ingress NetworkPolicy test needs
#      a real Ingress controller namespace on the cluster.
#   8. Deploy the in-cluster data stores (Postgres, Redis, MinIO),
#      wait for them to become Available, apply the schema-migration
#      Job, and create the MinIO artifact bucket.
#   9. Apply the lenny.dev CRDs and install the Lenny Helm chart with
#      the e2e values overlay.
#  10. Apply the agent-pod workload and wait for its pods to become
#      Ready.
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
INGRESS_NGINX_VERSION="controller-v1.11.3"

# Resolve the repository root from this script's location so the
# script runs correctly regardless of the caller's working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

KIND_CONFIG="${LENNY_CLUSTER_CONFIG:-${REPO_ROOT}/tests/testinfra/kind/cluster.yaml}"
E2E_VALUES="${REPO_ROOT}/tests/testinfra/kind/e2e-values.yaml"
DATASTORES_MANIFEST="${REPO_ROOT}/tests/testinfra/k8s/datastores.yaml"
MIGRATE_JOB_MANIFEST="${REPO_ROOT}/tests/testinfra/kind/migrate-job.yaml"
AGENT_WORKLOAD_MANIFEST="${REPO_ROOT}/tests/testinfra/kind/agent-workload.yaml"
CHART_DIR="${REPO_ROOT}/charts/lenny"

# Fixed in-cluster data-store facts. These mirror datastores.yaml and
# the postgres/redis/minio keys in e2e-values.yaml; the test code reads
# the stores at the same Service DNS names.
MINIO_BUCKET="lenny-artifacts"
MINIO_ACCESS_KEY="lennyminio"
MINIO_SECRET_KEY="lennyminio123"

# The platform binaries. Each maps to a cmd/<binary> package and is
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
  # §12.9.8 tier-9 egress-capture sidecar; the controller injects this
  # image when a Sandbox carries the egress-capture annotation.
  lenny-egress-capture
)

# The reference echo runtime images exercise the two §4.7 deployment
# models. lenny-runtime-echo runs the sidecar model: cmd/runtimes/echo
# is a stdin/stdout JSONL exec target and the lenny-adapter sidecar
# bridges it over an abstract Unix socket. lenny-runtime-echo-embedded
# runs the embedded model: cmd/runtimes/echo-embedded links the adapter
# into one container that serves the gRPC contract directly. Each entry
# is <image-base>=<cmd-path>; the cmd path differs from the image name,
# so these cannot reuse the BINARIES convention.
RUNTIME_IMAGES=(
  "lenny-runtime-echo=runtimes/echo"
  "lenny-runtime-echo-embedded=runtimes/echo-embedded"
  # §12.9.8 / §9.2 tier-9 probe runtimes. cred-shell-echo retains
  # /bin/sh for the credential-leakage probes; elicitation-echo
  # raises §9.2 elicitations through the platform MCP fabric.
  "lenny-runtime-cred-shell-echo=runtimes/cred-shell-echo"
  "lenny-runtime-elicitation-echo=runtimes/elicitation-echo"
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
# build_image <image-base> [cmd-path]. cmd-path defaults to image-base
# for the platform binaries whose image name matches their cmd/<name>
# package; the runtime images pass an explicit cmd-path.
build_image() {
  local image_base="$1"
  local cmd_path="${2:-$1}"
  local image="${image_base}:${TAG}"

  if docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} already built; skipping"
    return 0
  fi

  log "building ${image}"
  docker build --build-arg "BINARY=${cmd_path}" -t "${image}" "${REPO_ROOT}"

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} not found after build; retrying with DOCKER_BUILDKIT=0"
    DOCKER_BUILDKIT=0 docker build --build-arg "BINARY=${cmd_path}" -t "${image}" "${REPO_ROOT}"
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
  for entry in "${RUNTIME_IMAGES[@]}"; do
    build_image "${entry%%=*}" "${entry#*=}"
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
  for entry in "${RUNTIME_IMAGES[@]}"; do
    images+=("${entry%%=*}:${TAG}")
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
# Step 7: install ingress-nginx.
#
# The tier-5 gateway-ingress NetworkPolicy test (NET-038) asserts the
# chart's allow-gateway-ingress policy admits the Ingress controller
# namespace. That assertion needs a real Ingress controller installed
# on the cluster. The Kind-flavoured ingress-nginx manifest node-selects
# ingress-ready=true; cluster.yaml labels the control-plane node with
# it. This step is idempotent: it skips the apply when the controller
# Deployment is already rolled out. The step does not depend on the
# Helm chart and is independent of the data-store and chart steps below.
# ---------------------------------------------------------------------
if kc -n ingress-nginx get deploy ingress-nginx-controller >/dev/null 2>&1 &&
  [ "$(kc -n ingress-nginx get deploy ingress-nginx-controller \
    -o jsonpath='{.status.availableReplicas}' 2>/dev/null)" -ge 1 ] 2>/dev/null; then
  log "ingress-nginx controller already rolled out; skipping apply"
else
  log "installing ingress-nginx ${INGRESS_NGINX_VERSION}"
  kc apply -f "https://raw.githubusercontent.com/kubernetes/ingress-nginx/${INGRESS_NGINX_VERSION}/deploy/static/provider/kind/deploy.yaml"
fi
log "waiting for the ingress-nginx controller to become Available"
kc -n ingress-nginx wait --for=condition=Available deploy/ingress-nginx-controller --timeout=240s

# ---------------------------------------------------------------------
# Step 8: deploy the in-cluster data stores, migrate the schema, and
# create the MinIO bucket.
#
# The production chart treats Postgres, Redis, and MinIO as
# bring-your-own external services and deploys none of them.
# datastores.yaml is an e2e-only fixture: it stands up single-replica
# Postgres, Redis, and MinIO so the audit, backup, store-failure, and
# tenant-isolation tests have real stores. The gateway and controller
# are pointed at these Services by the postgres/redis/minio keys in
# e2e-values.yaml.
#
# Every sub-step is idempotent: `kubectl apply` reconciles the
# manifests, the migration Job is deleted before re-apply and
# `lenny-migrate up` is a no-op on an already-migrated database, and
# the MinIO bucket creation ignores an existing bucket.
# ---------------------------------------------------------------------
log "applying the in-cluster data-store manifests"
kc apply -f "${DATASTORES_MANIFEST}"

log "waiting for the data-store deployments to become Available"
for deploy in lenny-postgres lenny-redis lenny-minio; do
  kc -n lenny-system wait --for=condition=Available "deploy/${deploy}" --timeout=240s
done

# Run the schema-migration Job. lenny-migrate up applies the embedded
# migrations against the in-cluster Postgres. The Job is deleted first
# so a re-run of this script re-applies it cleanly; the migration
# itself is idempotent regardless.
log "running the lenny-migrate schema-migration Job"
kc -n lenny-system delete job lenny-e2e-migrate --ignore-not-found --wait=true
kc apply -f "${MIGRATE_JOB_MANIFEST}"
if ! kc -n lenny-system wait --for=condition=Complete job/lenny-e2e-migrate --timeout=180s; then
  log "the lenny-migrate Job did not complete; dumping its logs"
  kc -n lenny-system logs job/lenny-e2e-migrate || true
  echo "error: schema migration failed" >&2
  exit 1
fi
log "schema migration completed"

# Create the MinIO artifact bucket. The gateway's MinIO-backed artifact
# store and its drain-readiness probe expect the bucket to exist; the
# minio server does not create it. `mc mb --ignore-existing` is a no-op
# when the bucket is already present, so this step is idempotent. The
# mc client runs as a one-shot pod inside the cluster. It connects to
# the MinIO pod IP rather than the lenny-minio Service DNS name: the
# lenny-system default-deny NetworkPolicy denies a throwaway pod's
# egress to CoreDNS, so DNS resolution would time out. The
# allow-egress-to-e2e-datastores policy admits egress straight to the
# data-store pod IPs.
log "creating the MinIO artifact bucket ${MINIO_BUCKET}"
MINIO_POD_IP="$(kc -n lenny-system get pod -l lenny.dev/e2e-datastore=minio \
  -o jsonpath='{.items[0].status.podIP}')"
if [ -z "${MINIO_POD_IP}" ]; then
  echo "install.sh: could not resolve the lenny-minio pod IP" >&2
  exit 1
fi
kc -n lenny-system delete pod lenny-e2e-minio-mb --ignore-not-found --wait=true
kc -n lenny-system run lenny-e2e-minio-mb \
  --image=minio/mc:RELEASE.2024-09-16T17-43-14Z \
  --image-pull-policy=IfNotPresent \
  --restart=Never \
  --attach \
  --rm \
  --quiet \
  --command -- /bin/sh -c "
    set -e
    mc alias set e2e http://${MINIO_POD_IP}:9000 '${MINIO_ACCESS_KEY}' '${MINIO_SECRET_KEY}'
    mc mb --ignore-existing e2e/${MINIO_BUCKET}
  "
log "MinIO bucket ${MINIO_BUCKET} is present"

# ---------------------------------------------------------------------
# Step 9: apply the lenny.dev CRDs and install the Lenny Helm chart.
#
# Helm installs the chart's crds/ directory on `helm install` but never
# updates it on `helm upgrade`. Applying the CRDs here keeps a re-run
# against an existing release current with the in-tree CRD schemas, for
# example the Runtime deploymentModel field the agent workload in
# Step 10 depends on.
# ---------------------------------------------------------------------
log "applying the lenny.dev CRDs"
kc apply -f "${CHART_DIR}/crds/"

if helm status lenny -n lenny-system --kube-context "${KCTX}" >/dev/null 2>&1; then
  log "Helm release lenny is already installed in lenny-system; upgrading"
  helm upgrade lenny "${CHART_DIR}" \
    -n lenny-system \
    --kube-context "${KCTX}" \
    -f "${E2E_VALUES}" \
    --timeout 420s
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

# ---------------------------------------------------------------------
# Step 10: apply the agent-pod workload.
#
# agent-workload.yaml defines two SandboxWarmPools that exercise the
# two §4.7 deployment models: echo-pool-sidecar runs the runtime in a
# separate container bridged to the adapter over an abstract Unix
# socket, and echo-pool-embedded runs one container whose image embeds
# the adapter. The WarmPoolController reconciles each pool and the
# Sandbox reconciler produces real agent pods in lenny-agents, so the
# tier-5/8/9 tests that need a live agent pod run against a real
# workload rather than skipping. The chart's agent-namespaces template
# creates the lenny-agents namespace this workload lands in.
# ---------------------------------------------------------------------
log "applying the agent-pod workload"
kc apply -f "${AGENT_WORKLOAD_MANIFEST}"

log "waiting for the agent pods to become Ready"
for _ in $(seq 1 60); do
  running="$(kc -n lenny-agents get pods -l lenny.dev/managed=true \
    --no-headers 2>/dev/null | grep -c '.' || true)"
  [ "${running:-0}" -ge 2 ] && break
  sleep 5
done
if ! kc -n lenny-agents wait --for=condition=Ready pod \
  -l lenny.dev/managed=true --timeout=180s; then
  log "the agent pods did not become Ready; dumping their state"
  kc -n lenny-agents get pods -o wide || true
  echo "error: the agent-pod workload did not become Ready" >&2
  exit 1
fi

log "Lenny is installed on the ${CLUSTER} Kind cluster."
log "Run the tier-5 e2e suite with:"
log "  go test -tags=e2e_kind -count=1 ./tests/tier5_e2e_kind/..."
