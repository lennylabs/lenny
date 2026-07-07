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
#   0. Prune host-side Docker build cache and dangling images idle 24h+,
#      and each node's genuinely nameless (dangling) containerd images.
#      Guards against unbounded disk growth on this long-lived cluster
#      from tests that build or load uniquely-tagged images per run.
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
#   LENNY_FORCE_BUILD    When "1", rebuild every image even when a
#                        <name>:<tag> image already exists locally. The
#                        default skip-if-present shortcut reuses a stale
#                        image built from older source, which silently
#                        deploys out-of-date binaries after a code change;
#                        set this to guarantee the deployed images reflect
#                        the current tree. Ignored when LENNY_SKIP_BUILD=1.
#   LENNY_SKIP_PRUNE     When "1", skip the pre-build host and node prune
#                        step (see below). Default: prune runs every time.

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
APISERVER_EGRESS_MANIFEST="${REPO_ROOT}/tests/testinfra/kind/apiserver-egress.yaml"
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
  # §4.6.2 PoolScalingController runs as its own Deployment with a
  # dedicated leader lease; the chart references it by image, so it must
  # be built and loaded onto the offline Kind cluster like the rest.
  lenny-pool-scaling-controller
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
  # §6.1 SDK-warm reference runtime. preconnect-echo declares
  # capabilities.preConnect; the §6.3 startup_latency benchmark drives
  # its pool as the SDK-warm arm against the pod-warm echo pool.
  "lenny-runtime-preconnect-echo=runtimes/preconnect-echo"
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
# Step 0: prune stale host-side build cache and dangling images.
#
# This cluster is long-lived and re-installed/re-verified many times
# across sessions; a test that builds a uniquely-tagged image on every
# run (see tests/tier5_e2e_kind/runtime_publish_journey_test.go) leaves
# BuildKit cache entries that `docker rmi` never clears. Left unpruned
# across enough runs this silently exhausts the Docker Desktop VM disk
# (observed directly: a single day of repeated test-gaps batches against
# that journey test filled a 500GB+ VM disk). Time-scoped so a same-day
# rerun still benefits from a warm cache; only entries idle a day or
# more are reclaimed.
# ---------------------------------------------------------------------
if [[ "${LENNY_SKIP_PRUNE:-}" != "1" ]]; then
  log "pruning host docker build cache and dangling images older than 24h"
  docker builder prune -f --filter until=24h >/dev/null 2>&1 || true
  docker image prune -f --filter until=24h >/dev/null 2>&1 || true
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

  if [[ "${LENNY_FORCE_BUILD:-}" != "1" ]] && docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} already built; skipping (set LENNY_FORCE_BUILD=1 to rebuild)"
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
# Step 3b: prune genuinely dangling (nameless) images from each node's
# containerd store.
#
# A node's containerd content store only grows: `kind load docker-image`
# (used both by this script and directly by tests such as
# runtime_publish_journey_test.go, which loads a freshly-tagged image on
# every run) leaves an anonymous `import-<date>` alias alongside the
# intended name on every load, and nothing else on this long-lived
# cluster reclaims either. This deliberately does NOT use `crictl rmi
# --prune`: that flag removes every image with no CURRENTLY RUNNING
# container, which also catches images that are needed but only run
# on demand (Job images re-triggered later, reference-runtime images
# only pulled when a session actually uses them) and deletes their real
# tags -- observed directly deleting lenny-migrate:e2e,
# lenny-runtime-elicitation-echo:e2e, and others still needed by this
# cluster. Only an image with an EMPTY repoTags list (no human-readable
# name at all, matched by exact repoDigest reference so a same-content
# but properly-named sibling is never touched) is dangling in the sense
# this step targets. crictl ships inside the kind node image; python3
# is a pinned dev-environment tool (scripts/setup-dev.sh). Fall back to
# a no-op with a warning if either is missing rather than fail the
# install over housekeeping.
# ---------------------------------------------------------------------
if [[ "${LENNY_SKIP_PRUNE:-}" != "1" ]]; then
  if ! command -v python3 >/dev/null 2>&1; then
    log "WARNING: python3 not found; skipping node image prune"
  else
    nodes="$(kind get nodes --name "${CLUSTER}")"
    for node in ${nodes}; do
      if docker exec "${node}" which crictl >/dev/null 2>&1; then
        log "pruning dangling (untagged) containerd images on ${node}"
        refs="$(docker exec "${node}" crictl images -o json 2>/dev/null | python3 -c '
import json, sys
data = json.load(sys.stdin)
for img in data.get("images", []):
    if not img.get("repoTags"):
        digests = img.get("repoDigests") or []
        if digests:
            print(digests[0])
' || true)"
        if [[ -n "${refs}" ]]; then
          while IFS= read -r ref; do
            [[ -z "${ref}" ]] && continue
            docker exec "${node}" crictl rmi "${ref}" >/dev/null 2>&1 || true
          done <<<"${refs}"
        fi
      else
        log "WARNING: crictl not found on ${node}; skipping node image prune"
      fi
    done
  fi
fi

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
  # External prebuilt images the chart references that install.sh does not
  # build: the MinIO client the bucket lifecycle Job runs. Pull and load it
  # so the offline cluster's pullPolicy: Never resolves.
  #
  # The §13.2 dedicated CoreDNS image (ghcr.io/lennylabs/lenny-coredns) is
  # deliberately absent: e2e-values sets coredns.deploy=false, so the chart
  # renders no agent-dns Deployment. The image bundles the coredns-ratelimit
  # plugin, which has no public Go module source (github.com/coredns/ratelimit
  # 404s on the module proxy), so build/coredns-plugins cannot build offline,
  # and the private ghcr image cannot be pulled by the air-gapped kubelet.
  EXTERNAL_IMAGES=(
    "minio/mc:latest"
  )
  for img in "${EXTERNAL_IMAGES[@]}"; do
    if ! docker image inspect "${img}" >/dev/null 2>&1; then
      log "pulling external image ${img}"
      if ! docker pull "${img}"; then
        log "WARNING: could not pull ${img}; relying on a pre-loaded image on ${CLUSTER}"
        continue
      fi
    fi
    kind load docker-image --name "${CLUSTER}" "${img}" || \
      log "WARNING: could not load ${img} onto ${CLUSTER}; relying on a pre-loaded image"
  done
fi

# ---------------------------------------------------------------------
# Step 2b: resolve the runtime image digests.
#
# The §13.1 admission plane requires every Runtime CR and every §17.6
# bootstrap runtime image to be digest-pinned (`image@sha256:...`); a bare
# `:e2e` tag is rejected with a digest-pattern error. A local `docker
# build` assigns a fresh image id on every build, so a hardcoded digest
# goes stale the moment the image is rebuilt and the kubelet then reports
# ImagePullBackOff against a digest that no longer exists. install.sh
# therefore resolves each runtime image's current id at install time with
# `docker image inspect` and produces `<image>:<tag>@<digest>` references.
# These references feed both the Runtime CR placeholder substitution
# (Step 10) and the generated bootstrap overlay (Step 9), so every
# digest-pinned reference in the install tracks the freshly-built image.
#
# This runs regardless of LENNY_SKIP_BUILD: the images are present in the
# host Docker store either way (a skipped build assumes they are already
# built and loaded), and the digests are needed for both substitution
# paths below.
resolve_digest() {
  local image="$1"
  local id
  if ! id="$(docker image inspect "${image}" --format '{{.Id}}' 2>/dev/null)"; then
    echo "error: cannot resolve the digest of ${image}; is it built and loaded?" >&2
    exit 1
  fi
  if [[ -z "${id}" ]]; then
    echo "error: docker image inspect returned an empty id for ${image}" >&2
    exit 1
  fi
  # docker reports the id as `sha256:<hex>`, which is exactly the digest
  # suffix the `<image>@sha256:<hex>` reference form expects.
  printf '%s@%s' "${image}" "${id}"
}

log "resolving the runtime image digests"
ECHO_IMAGE="$(resolve_digest "lenny-runtime-echo:${TAG}")"
ECHO_EMBEDDED_IMAGE="$(resolve_digest "lenny-runtime-echo-embedded:${TAG}")"
PRECONNECT_ECHO_IMAGE="$(resolve_digest "lenny-runtime-preconnect-echo:${TAG}")"
CRED_SHELL_ECHO_IMAGE="$(resolve_digest "lenny-runtime-cred-shell-echo:${TAG}")"
ELICITATION_ECHO_IMAGE="$(resolve_digest "lenny-runtime-elicitation-echo:${TAG}")"
log "echo runtime image pinned to ${ECHO_IMAGE}"

# ---------------------------------------------------------------------
# Step 2c: register the digest-pinned reference in each node's containerd.
#
# The agent pods (and the §17.6 bootstrap runtimes) reference each runtime
# image by its digest-pinned form `<repo>:<tag>@sha256:<id>` because the
# §13.1 admission plane rejects a bare tag. `kind load docker-image`
# imports the image into containerd under its tag name only, so containerd
# has `lenny-runtime-echo:e2e` but no image named
# `lenny-runtime-echo@sha256:<id>`. When the kubelet resolves a pod's
# digest-pinned reference it looks up `<repo>@<digest>`, does not find it
# locally, and with imagePullPolicy IfNotPresent falls through to a
# registry pull that fails closed (ImagePullBackOff: the bare repo name
# resolves to docker.io/library and the image does not exist there). The
# id docker reports (`.Id`, the config digest) is the same value containerd
# records as the image's OCI index digest after a `kind load` import, so
# tagging the loaded image by its `<repo>@<id>` name on every node makes
# the digest-pinned reference resolve from the local store without a pull.
# This runs regardless of LENNY_SKIP_BUILD: the images are already loaded
# either way, and re-tagging an already-tagged image is idempotent.
log "registering digest-pinned runtime references in each node's containerd"
RUNTIME_DIGEST_REFS=(
  "${ECHO_IMAGE}"
  "${ECHO_EMBEDDED_IMAGE}"
  "${PRECONNECT_ECHO_IMAGE}"
  "${CRED_SHELL_ECHO_IMAGE}"
  "${ELICITATION_ECHO_IMAGE}"
)
for node in $(kind get nodes --name "${CLUSTER}"); do
  for ref in "${RUNTIME_DIGEST_REFS[@]}"; do
    # ref is `<repo>:<tag>@sha256:<id>`; the source tag is the part before
    # `@`, and the digest-pinned target containerd needs is
    # `docker.io/library/<repo>@sha256:<id>` (the kubelet's normalized
    # lookup form for a bare-repo digest reference).
    src_tag="${ref%@*}"
    repo="${src_tag%%:*}"
    digest="${ref#*@}"
    docker exec "${node}" ctr -n k8s.io images tag --force \
      "docker.io/library/${src_tag}" \
      "docker.io/library/${repo}@${digest}" >/dev/null 2>&1 || \
      log "WARNING: could not tag ${repo}@${digest} on ${node}; the pod may ImagePullBackOff"
  done
done

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

# §12.5 line 280 — the artifact-store TLS mandate binds the plaintext-MinIO
# exception to backends: embedded only; the e2e MinIO is a non-embedded
# backend, so datastores.yaml has cert-manager issue a serving cert for the
# lenny-minio Service DNS name and MinIO serves https on :9000. The chart
# mounts the issuing CA into the gateway and the bucket-lifecycle Job
# (minio.tls.caBundleConfigMap: lenny-minio-ca) and points SSL_CERT_DIR at
# it so both verify the endpoint. Copy the cert-manager-issued ca.crt out of
# the lenny-minio-tls Secret into that ConfigMap before the helm install.
log "waiting for the MinIO serving certificate to be issued"
kc -n lenny-system wait --for=condition=Ready certificate/lenny-minio-tls --timeout=120s
log "creating the lenny-minio-ca ConfigMap from the issued CA"
MINIO_CA_CRT="$(kc -n lenny-system get secret lenny-minio-tls -o jsonpath='{.data.ca\.crt}' | base64 -d)"
if [ -z "${MINIO_CA_CRT}" ]; then
  echo "error: lenny-minio-tls Secret has no ca.crt" >&2
  exit 1
fi
kc -n lenny-system create configmap lenny-minio-ca \
  --from-literal=ca.crt="${MINIO_CA_CRT}" \
  --dry-run=client -o yaml | kc apply -f -

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
# MinIO serves https on :9000 (§12.5 line 280; datastores.yaml issues the
# cert-manager serving cert). This one-shot pod dials the MinIO pod IP, which
# never matches the cert's DNS SAN, so mc runs with --insecure (skip TLS
# verification). The gateway and the chart's bucket-lifecycle Job verify the
# cert properly against the mounted CA; this throwaway bootstrap pod does not.
kc -n lenny-system run lenny-e2e-minio-mb \
  --image=minio/mc:RELEASE.2024-09-16T17-43-14Z \
  --image-pull-policy=IfNotPresent \
  --restart=Never \
  --attach \
  --rm \
  --quiet \
  --command -- /bin/sh -c "
    set -e
    mc --insecure alias set e2e https://${MINIO_POD_IP}:9000 '${MINIO_ACCESS_KEY}' '${MINIO_SECRET_KEY}'
    mc --insecure mb --ignore-existing e2e/${MINIO_BUCKET}
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

# Generate the §17.6 bootstrap overlay with the runtime digests resolved
# in Step 2b. The overlay carries the full bootstrap.runtimes and
# bootstrap.pools lists; Helm replaces a list value wholesale when a later
# -f sets it, so these lists supersede the e2e-values.yaml bootstrap block
# (which intentionally declares neither) while the tenants/users maps there
# deep-merge through. The runtimes are the §4.7 reference set the tier-5/8
# sessiondriver harness drives by name plus the generic `echo` /
# `claude-code` runtimeRefs the tier-7 scenarios post; the pools are the
# Postgres-authoritative pool rows the PoolScalingController reconciles
# into SandboxTemplate/SandboxWarmPool CRD pairs in lenny-agents. Each pool
# is standard-isolation (the §13.1 Kind cluster has no Kata/microVM runtime
# class) with allowStandardIsolation: true, the §5.3 deployer opt-in a
# standard-isolation pool requires. Each pool also sets the §13.2 per-pool
# DNS opt-out (dnsPolicy: cluster-default): the dedicated lenny-system
# CoreDNS instance is disabled on this overlay (coredns.deploy=false,
# because the lenny-coredns image is unbuildable offline), so the
# WarmPoolController stamps the lenny.dev/dns-policy: cluster-default label
# and the supplemental allow-pod-egress-kube-dns NetworkPolicy (rendered by
# coredns.allowClusterDefaultOptOut in e2e-values.yaml) admits the pods'
# kube-system DNS egress. spec: §17.6, §4.6.2, §13.1, §13.2.
BOOTSTRAP_OVERLAY="${REPO_ROOT}/tests/testinfra/kind/bootstrap-overlay.gen.yaml"
log "generating the bootstrap overlay with resolved runtime digests"
cat >"${BOOTSTRAP_OVERLAY}" <<EOF
# Generated by install.sh; do not edit. Runtime images are digest-pinned
# to the freshly-built lenny-runtime-*:e2e images (resolved at install
# time with docker image inspect). Regenerate by re-running install.sh.
bootstrap:
  runtimes:
    # Generic runtimeRefs the tier-7 load scenarios post as session
    # runtimeRef; both resolve to the echo image.
    - name: echo
      type: agent
      image: ${ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: echo
    - name: claude-code
      type: agent
      image: ${ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: claude-code
    # The §4.7 sidecar / embedded / SDK-warm reference runtimes the
    # tier-5/8 sessiondriver harness drives by name. The gateway runtime
    # lookup resolves these when a test posts POST /v1/sessions with the
    # matching runtimeRef; the Sandbox reconciler reads the same-named
    # Runtime CR (applied from agent-workload.yaml) for the pod image.
    # capabilities.injection.supported is true (§5.1) to match the
    # Runtime CRD in agent-workload.yaml, so the tier5/8/9
    # sessiondriver harness can drive a mid-session message round trip
    # against a real echo-runtime-sidecar pod.
    - name: echo-runtime-sidecar
      type: agent
      image: ${ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: echo-sidecar
    - name: echo-runtime-embedded
      type: agent
      image: ${ECHO_EMBEDDED_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: echo-embedded
    # §6.1 SDK-warm runtime (capabilities.preConnect): the §6.3
    # startup_latency benchmark's SDK-warm arm.
    - name: preconnect-echo-runtime
      type: agent
      image: ${PRECONNECT_ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      capabilities:
        preConnect: true
      labels:
        lenny.dev/e2e: preconnect-echo
    - name: cred-shell-echo-runtime
      type: agent
      image: ${CRED_SHELL_ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: cred-shell-echo
    - name: elicitation-echo-runtime
      type: agent
      image: ${ELICITATION_ECHO_IMAGE}
      integrationLevel: standard
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: elicitation-echo
  pools:
    # §6.3 pod-warm arm: a standard/session pool backed by the sidecar
    # echo runtime. warmCount is 6 (not 1) so the §6.3 startup benchmark's
    # 2-VU smoke run finds an idle pod on every claim. Each iteration
    # claims a pod and terminates the session to release it, but on Kind a
    # released pod takes several seconds to scrub and return to idle and a
    # warmCount-1 pool refills a freshly-created replacement just as
    # slowly, so a single warm pod exhausts within the first burst and ~96%
    # of POST /v1/sessions/start calls fail with no-idle-pod. A depth of 6
    # holds enough idle inventory to absorb the in-flight claims plus the
    # refill lag; the echo pods request ~128Mi each, well within the Kind
    # nodes' headroom.
    - name: echo-pool-sidecar
      runtimeRef: echo-runtime-sidecar
      isolationProfile: standard
      executionMode: session
      warmCount: 6
      allowStandardIsolation: true
      dnsPolicy: cluster-default
    - name: echo-pool-embedded
      runtimeRef: echo-runtime-embedded
      isolationProfile: standard
      executionMode: session
      warmCount: 1
      allowStandardIsolation: true
      dnsPolicy: cluster-default
    # §6.3 SDK-warm arm: the preconnect runtime drives the warming ->
    # sdk_connecting -> idle path. warmCount is 6 for the same reason as
    # echo-pool-sidecar above: the benchmark's 2-VU smoke run exhausts a
    # single warm pod, and the SDK-warm refill (warming -> sdk_connecting
    # -> idle) is slower still, so the depth keeps an idle pod available
    # for every claim.
    - name: preconnect-echo-pool
      runtimeRef: preconnect-echo-runtime
      isolationProfile: standard
      executionMode: session
      warmCount: 6
      allowStandardIsolation: true
      dnsPolicy: cluster-default
    - name: cred-shell-echo-pool
      runtimeRef: cred-shell-echo-runtime
      isolationProfile: standard
      executionMode: session
      warmCount: 1
      allowStandardIsolation: true
      dnsPolicy: cluster-default
    - name: elicitation-echo-pool
      runtimeRef: elicitation-echo-runtime
      isolationProfile: standard
      executionMode: session
      warmCount: 1
      allowStandardIsolation: true
      dnsPolicy: cluster-default
EOF

# --server-side=false forces client-side apply. Helm 4 defaults to
# server-side apply, whose strict (containerPort, protocol) list-map key
# rejects the chart's named http+metrics ports sharing one containerPort
# (§16.9 line 723); helm 3 used client-side apply and tolerated it. The
# Kind e2e pins client-side apply so it installs identically under helm 3
# and helm 4 without depending on the chart's server-side-apply posture.
# The generated bootstrap overlay is passed last so its bootstrap.runtimes
# and bootstrap.pools lists win over the e2e-values.yaml bootstrap block.
if helm status lenny -n lenny-system --kube-context "${KCTX}" >/dev/null 2>&1; then
  log "Helm release lenny is already installed in lenny-system; upgrading"
  helm upgrade lenny "${CHART_DIR}" \
    -n lenny-system \
    --kube-context "${KCTX}" \
    -f "${E2E_VALUES}" \
    -f "${BOOTSTRAP_OVERLAY}" \
    --server-side=false \
    --timeout 420s
else
  log "installing the Lenny Helm chart"
  helm install lenny "${CHART_DIR}" \
    -n lenny-system \
    --kube-context "${KCTX}" \
    -f "${E2E_VALUES}" \
    -f "${BOOTSTRAP_OVERLAY}" \
    --server-side=false \
    --timeout 420s
fi

# The §4.6.2 PoolScalingController's egress to the kube-apiserver, PgBouncer,
# and kube-system CoreDNS is now admitted by the chart's
# allow-pool-scaling-controller-egress NetworkPolicy (rendered when
# postgres.dsn is set, which the e2e overlay sets), so no test-infra
# PSC-egress manifest is applied here. On Kind the apiserver dial additionally
# needs the post-DNAT node-CIDR egress that allow-lenny-system-apiserver-egress
# below admits for every lenny-system pod, and the PSC reaches the in-cluster
# Postgres datastore through allow-egress-to-e2e-datastores in datastores.yaml.

# Admit lenny-system egress to the Kind kube-apiserver. kindnet evaluates
# the egress NetworkPolicy against the post-DNAT destination, so the chart
# policies' service-CIDR ipBlock (10.96.0.0/12, covering the kubernetes
# Service ClusterIP) does not match the apiserver's real endpoint on the
# `kind` Docker bridge network (172.18.0.0/16). Without this the gateway's
# per-session SandboxClaim write times out and POST /v1/sessions/start
# returns 503 SESSION_CREATION_FAILED. The manifest is test-infra-only, so
# it does not alter the production chart rendering.
log "applying the kube-apiserver egress NetworkPolicy"
kc apply -f "${APISERVER_EGRESS_MANIFEST}"

# Wait for the core control-plane pods to become Ready. The dedicated
# §13.2 CoreDNS instance is disabled on this overlay (e2e-values sets
# coredns.deploy=false), so no lenny.dev/component: coredns pods are
# rendered; the Ready wait covers the gateway, controller, token-service,
# and webhooks. The component!=coredns selector clause is retained as a
# guard so a future re-enable of the dedicated instance does not silently
# block the install on a CoreDNS image gap.
log "waiting for the lenny-system control-plane pods to become Ready"
kc -n lenny-system wait --for=condition=Ready pod \
  -l app.kubernetes.io/name=lenny,lenny.dev/component!=coredns \
  --timeout=300s

# ---------------------------------------------------------------------
# Step 10: apply the agent Runtime CRs and wait for the warm pods.
#
# agent-workload.yaml defines the §4.7 reference Runtime CRs and the runc
# RuntimeClass. The pools themselves are Postgres-authoritative: the §17.6
# bootstrap.pools list seeded through the chart (Step 9) creates the pool
# rows, and the PoolScalingController reconciles each into a
# SandboxTemplate + SandboxWarmPool CRD pair in lenny-agents. The Sandbox
# reconciler reads each pool's Runtime CR spec.image to build the warm
# agent pods, so the tier-5/8/9 tests that need a live agent pod run
# against a real workload rather than skipping. The chart's
# agent-namespaces template creates the lenny-agents namespace.
#
# The Runtime CR images carry __*_IMAGE__ placeholders so the manifest
# never embeds a stale digest. Substitute the digests resolved in Step 2b
# before applying; the §13.1 admission plane rejects a bare :e2e tag, so
# every image must be the pinned image:e2e@sha256:<digest> form.
# ---------------------------------------------------------------------
log "applying the agent Runtime CRs (digest-pinned)"
sed \
  -e "s|__ECHO_IMAGE__|${ECHO_IMAGE}|g" \
  -e "s|__ECHO_EMBEDDED_IMAGE__|${ECHO_EMBEDDED_IMAGE}|g" \
  -e "s|__PRECONNECT_ECHO_IMAGE__|${PRECONNECT_ECHO_IMAGE}|g" \
  -e "s|__CRED_SHELL_ECHO_IMAGE__|${CRED_SHELL_ECHO_IMAGE}|g" \
  -e "s|__ELICITATION_ECHO_IMAGE__|${ELICITATION_ECHO_IMAGE}|g" \
  "${AGENT_WORKLOAD_MANIFEST}" | kc apply -f -

# The pools reach Postgres only after the post-install lenny-bootstrap Job
# runs, and the PoolScalingController then reconciles them into CRDs and
# the warm pods are created and pass their readiness probe. That whole
# chain runs asynchronously after `helm install` returns, so poll for the
# warm pods rather than expecting them immediately. The §6.3 startup
# benchmark needs a warm pod from the echo-pool-sidecar (pod-warm) and
# preconnect-echo-pool (SDK-warm) pools, so wait until at least two managed
# pods report Ready. This is a positive threshold on purpose rather than an
# all-managed-pods-Ready wait: a persistent cluster accumulates leftover
# managed pods from earlier test runs (for example chaos pools whose
# ephemeral runtime images were pruned and now sit in ImagePullBackOff), and
# those never become Ready. A blanket `kubectl wait ... Ready pod` over the
# lenny.dev/managed=true selector blocks on them and fails the install even
# though the freshly-installed pools are healthy. Counting Ready pods
# tolerates those leftovers and gates only on two healthy managed pods.
log "waiting for at least two warm agent pods to become Ready (pools warm asynchronously)"
ready=0
for _ in $(seq 1 36); do
  ready="$(kc -n lenny-agents get pods -l lenny.dev/managed=true \
    -o 'jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' \
    2>/dev/null | grep -c '^True$' || true)"
  [ "${ready:-0}" -ge 2 ] && break
  sleep 5
done
if [ "${ready:-0}" -lt 2 ]; then
  log "fewer than two warm agent pods became Ready; dumping their state"
  kc -n lenny-agents get sandboxwarmpool,pods -o wide || true
  echo "error: fewer than two warm agent pods became Ready" >&2
  exit 1
fi
log "${ready} warm agent pod(s) Ready"

log "Lenny is installed on the ${CLUSTER} Kind cluster."
log "Run the tier-5 e2e suite with:"
log "  go test -tags=e2e_kind -count=1 ./tests/tier5_e2e_kind/..."
