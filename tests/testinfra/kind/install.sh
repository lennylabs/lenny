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
#      Old content tags are pruned from the host and the nodes as well.
#   1. Compute each image's content tag, and build the platform binary
#      images, the reference runtime images, and the test fixture images
#      whose content tag is not yet present locally, each as a separate
#      `docker build` process.
#   2. Load onto the Kind cluster nodes the images a node lacks.
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
# Image tags:
#   By default every image is tagged with a content tag, src-<12 hex
#   digits>, that tests/testinfra/kind/imagetag computes from the image's
#   build inputs: its Dockerfile, its build arguments, the context files
#   its COPY instructions name, the Go package closure of the binary it
#   compiles (go list -deps, including go.mod, go.sum, and embedded
#   files), and the content tags of the images it builds on. The hash
#   reads the working tree, so uncommitted and untracked changes count.
#   An image whose content tag is already present locally is reused
#   rather than rebuilt, and an image a node already holds under its
#   content tag is not reloaded onto that node. A change to any input
#   changes the tag, so the changed image is rebuilt and reloaded, and
#   the chart's Deployments roll out because the rendered image reference
#   changed. The base images the Dockerfiles name (golang, distroless)
#   are unpinned tags and are not hashed; set LENNY_FORCE_BUILD=1 after
#   pulling a newer base image.
#
#   Every consumer this script renders (the chart values, the bootstrap
#   overlay, the Runtime CRs, and the migration Job) receives the content
#   tag. Each loaded image also carries the moving alias tag
#   <name>:${LENNY_ALIAS_TAG:-e2e}, locally and on every node, because
#   tests and fixtures outside this script name images such as
#   lenny-adapter:e2e and lenny-migrate:e2e directly.
#
# Environment variables:
#   LENNY_KIND_CLUSTER   Cluster name. Default: lenny-e2e.
#   LENNY_IMAGE_TAG      When set, tag every image with this fixed tag
#                        instead of a content tag, and skip building an
#                        image whose <name>:<tag> already exists locally.
#                        That shortcut deploys a stale image after a
#                        source change unless LENNY_FORCE_BUILD=1 is set.
#   LENNY_ALIAS_TAG      The moving alias tag applied to each loaded
#                        image in content-tag mode. Default: e2e.
#   LENNY_SKIP_BUILD     When "1", skip the image build, load, and
#                        content-tag prune steps, and deploy the images
#                        already loaded under the alias tag (or under
#                        LENNY_IMAGE_TAG when it is set).
#   LENNY_FORCE_BUILD    When "1", rebuild every image even when its tag
#                        already exists locally, and hand every image to
#                        `kind load`, which reloads a node whose copy has a
#                        different image ID. Ignored when LENNY_SKIP_BUILD=1.
#   LENNY_SKIP_PRUNE     When "1", skip the host and node prune steps (see
#                        below). Default: prune runs every time.
#   LENNY_KEEP_CONTENT_TAGS
#                        How many content tags per image the host keeps
#                        besides the current one. Default: 3.

set -euo pipefail

CLUSTER="${LENNY_KIND_CLUSTER:-lenny-e2e}"
ALIAS_TAG="${LENNY_ALIAS_TAG:-e2e}"
KEEP_CONTENT_TAGS="${LENNY_KEEP_CONTENT_TAGS:-3}"
# FIXED_TAG is empty in content-tag mode. LENNY_SKIP_BUILD=1 deploys what
# is already loaded, which carries the alias tag.
if [[ -n "${LENNY_IMAGE_TAG:-}" ]]; then
  FIXED_TAG="${LENNY_IMAGE_TAG}"
elif [[ "${LENNY_SKIP_BUILD:-}" == "1" ]]; then
  FIXED_TAG="${ALIAS_TAG}"
else
  FIXED_TAG=""
fi
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

# IMAGE_SPECS is the image inventory, one image per line, in the format
# tests/testinfra/kind/imagetag parses:
#
#   <name> <dockerfile> [--context=<dir>] [--no-load] [KEY=VALUE ...]
#
# The Dockerfile path and the context are relative to the repository
# root, and the context defaults to it. KEY=VALUE is a build argument; a
# value of the form @<name> names another image in this list and is
# replaced by that image's <name>:<tag> reference, so the referencing
# image builds after it and its content tag covers the named image's tag.
# --no-load marks a build-only base that is not loaded onto the cluster.
# tests/testinfra/kind/imagetag/install_specs_test.go parses this block
# and computes its tags at tier 1, so a line naming a missing Dockerfile,
# package, or image fails there before an install does.
# read -d '' returns 1 at the end of the here-document, hence || true.
read -r -d '' IMAGE_SPECS <<'SPECS' || true
# The platform binaries, built from the root Dockerfile. Each maps to a
# cmd/<binary> package.
lenny-controller Dockerfile BINARY=lenny-controller
lenny-gateway Dockerfile BINARY=lenny-gateway
lenny-webhook Dockerfile BINARY=lenny-webhook
lenny-token-service Dockerfile BINARY=lenny-token-service
lenny-ops Dockerfile BINARY=lenny-ops
lenny-migrate Dockerfile BINARY=lenny-migrate
lenny-preflight Dockerfile BINARY=lenny-preflight
lenny-backup Dockerfile BINARY=lenny-backup
lenny-ctl Dockerfile BINARY=lenny-ctl
# §4.6.2 PoolScalingController runs as its own Deployment with a
# dedicated leader lease; the chart references it by image, so it must be
# built and loaded onto the offline Kind cluster like the rest.
lenny-pool-scaling-controller Dockerfile BINARY=lenny-pool-scaling-controller
# TESTING.md §12.9.8 tier-9 egress-capture sidecar; the controller injects
# this image when a Sandbox carries the egress-capture annotation.
lenny-egress-capture Dockerfile BINARY=lenny-egress-capture
# The adapter binary image. It is a build base only: the lenny-adapter
# image below overlays the supervise-probe binary onto it.
lenny-adapter-base Dockerfile BINARY=lenny-adapter --no-load

# The reference runtime images exercise the two §4.7 deployment models.
# lenny-runtime-echo runs the sidecar model: cmd/runtimes/echo is a
# stdin/stdout JSONL exec target and the lenny-adapter sidecar bridges it
# over an abstract Unix socket. lenny-runtime-echo-embedded runs the
# embedded model: cmd/runtimes/echo-embedded links the adapter into one
# container that serves the gRPC contract directly.
lenny-runtime-echo Dockerfile BINARY=runtimes/echo
lenny-runtime-echo-embedded Dockerfile BINARY=runtimes/echo-embedded
# §6.1 SDK-warm reference runtime. preconnect-echo declares
# capabilities.preConnect; the §6.3 startup_latency benchmark drives its
# pool as the SDK-warm arm against the pod-warm echo pool.
lenny-runtime-preconnect-echo Dockerfile BINARY=runtimes/preconnect-echo
# TESTING.md §12.9.8 / §9.2 tier-9 probe runtimes. cred-shell-echo retains
# /bin/sh for the credential-leakage probes; elicitation-echo raises §9.2
# elicitations through the platform MCP fabric.
lenny-runtime-cred-shell-echo Dockerfile BINARY=runtimes/cred-shell-echo
lenny-runtime-elicitation-echo Dockerfile BINARY=runtimes/elicitation-echo
# §5.2 sessionId dispatch-loop reference runtime. Backs
# concurrent-echo-pool (sessionPolicy.maxConcurrentSessions > 1), the
# tier-5 real-pod counterpart of the tier-4 concurrent_workspace_test.go
# in-process flow.
lenny-runtime-echo-concurrent Dockerfile BINARY=runtimes/echo-concurrent

# Test-only fixture images. tests/testinfra/heartbeatsilence/Dockerfile
# layers the heartbeat-silence fixture on top of the named reference
# runtime image, so the reference binary runs unchanged behind a filter
# that stops forwarding heartbeats after the fixture's directive. Only the
# heartbeat-silence pools below use these images; they back the tier-5
# stream-failure cases in tests/tier5_e2e_kind/stream_failure_kind_test.go.
lenny-runtime-heartbeat-silence-echo tests/testinfra/heartbeatsilence/Dockerfile BASE_IMAGE=@lenny-runtime-echo
lenny-runtime-heartbeat-silence-echo-concurrent tests/testinfra/heartbeatsilence/Dockerfile BASE_IMAGE=@lenny-runtime-echo-concurrent

# lenny-supervisor is the runtime supervisor every sidecar pod of a
# type: agent runtime stages into its read-only supervisor volume (§4.7.10
# Runtime supervisor; §13.1 Runtime supervisor volume); the chart values
# overlay names it as controller.supervisorImage, and without it the
# controller creates no pod for any such pool.
lenny-supervisor Dockerfile.supervisor
# lenny-runtime-supervise-probe is the test-only probe runtime
# (tests/testinfra/superviseprobe) the supervisor's cluster tiers run; its
# image sets an ENV variable no Runtime env sets and STOPSIGNAL SIGINT.
lenny-runtime-supervise-probe cmd/runtimes/supervise-probe/Dockerfile

# The e2e adapter image: lenny-adapter-base with the supervise-probe binary
# added as /usr/local/bin/lenny-setup-probe, the setup command the
# supervised scrub's setup-residue case runs in the adapter container
# (§5.2 Supervised scrub sequence).
lenny-adapter cmd/runtimes/supervise-probe/adapter-overlay.Dockerfile --context=cmd/runtimes/supervise-probe BASE_IMAGE=@lenny-adapter-base PROBE_IMAGE=@lenny-runtime-supervise-probe
SPECS

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
require go

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
# Step 1: compute the image tags and build the images.
#
# image-tags prints one line per image in build order: name, tag, load
# flag (1 or 0), Dockerfile, context, and the build arguments with every
# @<name> reference resolved to <name>:<tag>. In content-tag mode the tag
# is the image's content tag; with LENNY_IMAGE_TAG set, or with
# LENNY_SKIP_BUILD=1, it is FIXED_TAG for every image.
# ---------------------------------------------------------------------
log "computing image tags"
IMAGE_PLAN="$(printf '%s\n' "${IMAGE_SPECS}" | (
  cd "${REPO_ROOT}" &&
    go run ./tests/testinfra/kind/imagetag/cmd/image-tags --root "${REPO_ROOT}" ${FIXED_TAG:+--tag "${FIXED_TAG}"}
))"

# image_tag <name> prints the tag this install deploys for <name>.
image_tag() {
  local tag
  tag="$(awk -F'\t' -v n="$1" '$1 == n { print $2 }' <<<"${IMAGE_PLAN}")"
  if [[ -z "${tag}" ]]; then
    echo "error: image $1 is not in IMAGE_SPECS" >&2
    exit 1
  fi
  printf '%s' "${tag}"
}

# image_ref <name> prints <name>:<tag> for the tag this install deploys.
image_ref() { printf '%s:%s' "$1" "$(image_tag "$1")"; }

# LOADED_REFS are the <name>:<tag> references of the images loaded onto
# the cluster, and LOADED_NAMES their names.
LOADED_REFS=()
LOADED_NAMES=()
while IFS=$'\t' read -r name tag load _; do
  if [[ "${load}" == "1" ]]; then
    LOADED_REFS+=("${name}:${tag}")
    LOADED_NAMES+=("${name}")
  fi
done <<<"${IMAGE_PLAN}"

# Each image is built with its own `docker build` invocation. A shell
# `for` loop that redirects output does not land the image in this
# host's Docker Desktop daemon store, so every build runs as its own
# foreground process with output going to the terminal. If an image
# does not appear in `docker images` after a build, the build falls
# back to DOCKER_BUILDKIT=0, which writes to the legacy image store.
#
# build_image <name> <tag> <dockerfile> <context> [KEY=VALUE ...] builds
# <name>:<tag> unless that tag already exists locally and
# LENNY_FORCE_BUILD is not "1". In content-tag mode an existing tag holds
# an image built from the same inputs, so skipping it is safe.
build_image() {
  local image="$1:$2"
  local dockerfile_rel="$3"
  local dockerfile="${REPO_ROOT}/$3"
  local context="${REPO_ROOT}/$4"
  shift 4
  local build_args=()
  local arg
  for arg in "$@"; do
    build_args+=(--build-arg "${arg}")
  done

  if [[ "${LENNY_FORCE_BUILD:-}" != "1" ]] && docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} already built; skipping"
    return 0
  fi

  log "building ${image} from ${dockerfile_rel}"
  docker build -f "${dockerfile}" "${build_args[@]}" -t "${image}" "${context}" </dev/null

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    log "image ${image} not found after build; retrying with DOCKER_BUILDKIT=0"
    DOCKER_BUILDKIT=0 docker build -f "${dockerfile}" "${build_args[@]}" -t "${image}" "${context}" </dev/null
  fi

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    echo "error: image ${image} is still absent after both build attempts" >&2
    exit 1
  fi
}

# is_content_tag <tag> reports whether <tag> has the content-tag form
# src-<12 hex digits> that imagetag.IsContentTag matches. Only tags of
# this form are pruned; e2e, e2e-journey, and other fixed tags are never
# touched.
is_content_tag() { [[ "$1" =~ ^src-[0-9a-f]{12}$ ]]; }

if [[ "${LENNY_SKIP_BUILD:-}" == "1" ]]; then
  log "LENNY_SKIP_BUILD=1; skipping image build and load; deploying tag ${FIXED_TAG}"
else
  while IFS=$'\t' read -r name tag load dockerfile context args; do
    # args is a space-separated KEY=VALUE list with no embedded spaces.
    # shellcheck disable=SC2086
    build_image "${name}" "${tag}" "${dockerfile}" "${context}" ${args}
  done <<<"${IMAGE_PLAN}"

  if [[ -z "${FIXED_TAG}" ]]; then
    # Point the moving alias tag at this install's images, for the tests
    # and fixtures that name <name>:e2e directly.
    for ref in "${LOADED_REFS[@]}"; do
      docker tag "${ref}" "${ref%%:*}:${ALIAS_TAG}"
    done

    # Prune old content tags on the host. Per image, keep the tag this
    # install deploys and the KEEP_CONTENT_TAGS most recently created
    # other content tags, so switching between a few revisions reuses
    # their images, and remove the rest. `docker image ls` lists newest
    # first. `docker rmi` without -f refuses an image a container uses,
    # and the failure is ignored.
    if [[ "${LENNY_SKIP_PRUNE:-}" != "1" ]]; then
      while IFS=$'\t' read -r name tag _; do
        kept=0
        for old in $(docker image ls --format '{{.Tag}}' "${name}"); do
          is_content_tag "${old}" || continue
          [[ "${old}" == "${tag}" ]] && continue
          if ((kept < KEEP_CONTENT_TAGS)); then
            kept=$((kept + 1))
            continue
          fi
          log "pruning old content tag ${name}:${old}"
          docker rmi "${name}:${old}" >/dev/null 2>&1 || true
        done
      done <<<"${IMAGE_PLAN}"
    fi
  fi
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
# Step 3a: install the §5.3 gVisor RuntimeClass onto the sandbox-gvisor
# labelled node (idempotent; see install-gvisor.sh). Runs immediately
# after cluster creation and before any workload exists on the node, so
# the containerd restart it performs on a first install never disturbs
# a running agent pod. Set LENNY_KIND_SKIP_GVISOR=1 to opt out on a host
# with no route to the gVisor release bucket; the sandboxed pool below
# then stays Degraded and gvisor_isolation_test.go skips cleanly.
# ---------------------------------------------------------------------
LENNY_KIND_CLUSTER="${CLUSTER}" "${SCRIPT_DIR}/install-gvisor.sh"

# ---------------------------------------------------------------------
# Step 3b: prune old content tags from each node's containerd store.
#
# In content-tag mode each source change loads a new <name>:src-<hash>
# image onto the nodes, so the older ones accumulate. A content tag of an
# image in IMAGE_SPECS is removed from a node when this install does not
# deploy it and no container on the node, running or exited, uses its
# image. Removing the tag leaves the image nameless once the alias tag has
# moved to the current image, and Step 3c below (or the next install's
# Step 3c, while a digest reference still names it) removes it. Nodes keep
# no older revisions, because reloading one costs seconds. A name outside
# IMAGE_SPECS and a fixed tag such as e2e or e2e-journey are never
# touched.
# ---------------------------------------------------------------------
if [[ "${LENNY_SKIP_PRUNE:-}" != "1" && "${LENNY_SKIP_BUILD:-}" != "1" && -z "${FIXED_TAG}" ]]; then
  if ! command -v python3 >/dev/null 2>&1; then
    log "WARNING: python3 not found; skipping node content-tag prune"
  else
    prune_dir="$(mktemp -d)"
    awk -F'\t' '{ print "docker.io/library/" $1 ":" $2 }' <<<"${IMAGE_PLAN}" >"${prune_dir}/keep"
    awk -F'\t' '{ print "docker.io/library/" $1 }' <<<"${IMAGE_PLAN}" >"${prune_dir}/names"
    for node in $(kind get nodes --name "${CLUSTER}"); do
      if ! docker exec "${node}" which crictl >/dev/null 2>&1; then
        log "WARNING: crictl not found on ${node}; skipping node content-tag prune"
        continue
      fi
      docker exec "${node}" crictl images -o json >"${prune_dir}/images.json" 2>/dev/null || continue
      docker exec "${node}" crictl ps -a -o json >"${prune_dir}/containers.json" 2>/dev/null || continue
      stale="$(python3 - "${prune_dir}" <<'PY' || true
import json, os, re, sys
d = sys.argv[1]
keep = set(open(os.path.join(d, "keep")).read().split())
names = set(open(os.path.join(d, "names")).read().split())
images = json.load(open(os.path.join(d, "images.json"))).get("images", [])
containers = json.load(open(os.path.join(d, "containers.json"))).get("containers", [])
in_use = set()
for c in containers:
    for key in ("imageRef", "imageId"):
        if c.get(key):
            in_use.add(c[key])
    image = (c.get("image") or {}).get("image")
    if image:
        in_use.add(image)
content = re.compile(r"^(.*):src-[0-9a-f]{12}$")
for img in images:
    if img.get("id") in in_use:
        continue
    for tag in img.get("repoTags") or []:
        m = content.match(tag)
        if m and m.group(1) in names and tag not in keep:
            print(tag)
PY
)"
      while IFS= read -r ref; do
        [[ -z "${ref}" ]] && continue
        log "pruning old content tag ${ref} on ${node}"
        docker exec "${node}" ctr -n k8s.io images rm "${ref}" >/dev/null 2>&1 || true
      done <<<"${stale}"
    done
    rm -rf "${prune_dir}"
  fi
fi

# ---------------------------------------------------------------------
# Step 3c: prune genuinely dangling (nameless) images from each node's
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
#
# `kind load docker-image` saves every image it is given into one archive
# whenever any node lacks any of them, so handing it the full list
# re-exports and re-imports every image after a one-image change. In
# content-tag mode a node that lists <name>:src-<hash> holds exactly that
# image, so the list is narrowed to the images some node lacks, and kind
# loads each onto the nodes that lack it. A load interrupted part-way
# leaves the remaining nodes without the tag, and the next install loads
# them. With LENNY_FORCE_BUILD=1 or a fixed tag the same tag may name
# new content, so every image is handed to kind, which compares image IDs
# per node.
# ---------------------------------------------------------------------
if [[ "${LENNY_SKIP_BUILD:-}" != "1" ]]; then
  nodes="$(kind get nodes --name "${CLUSTER}")"
  to_load=()
  if [[ -z "${FIXED_TAG}" && "${LENNY_FORCE_BUILD:-}" != "1" ]]; then
    node_refs=""
    for node in ${nodes}; do
      node_refs+="$(docker exec "${node}" ctr -n k8s.io images ls -q 2>/dev/null | sed "s|^|${node} |")"$'\n'
    done
    for ref in "${LOADED_REFS[@]}"; do
      for node in ${nodes}; do
        if ! grep -qxF "${node} docker.io/library/${ref}" <<<"${node_refs}"; then
          to_load+=("${ref}")
          break
        fi
      done
    done
  else
    to_load=("${LOADED_REFS[@]}")
  fi
  if ((${#to_load[@]} > 0)); then
    log "loading ${#to_load[@]} of ${#LOADED_REFS[@]} images onto cluster ${CLUSTER}: ${to_load[*]}"
    kind load docker-image --name "${CLUSTER}" "${to_load[@]}"
  else
    log "every node already holds all ${#LOADED_REFS[@]} images; nothing to load"
  fi

  if [[ -z "${FIXED_TAG}" ]]; then
    # Point each node's alias tag at this install's images, for the tests
    # and fixtures that name <name>:e2e directly. One exec per node.
    alias_cmds=""
    for ref in "${LOADED_REFS[@]}"; do
      alias_cmds+="ctr -n k8s.io images tag --force docker.io/library/${ref} docker.io/library/${ref%%:*}:${ALIAS_TAG} >/dev/null && "
    done
    for node in ${nodes}; do
      docker exec "${node}" sh -c "${alias_cmds}true" ||
        log "WARNING: could not move the ${ALIAS_TAG} alias tags on ${node}"
    done
  fi

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
# tag is rejected with a digest-pattern error. A local `docker
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
ECHO_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-echo)")"
ECHO_EMBEDDED_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-echo-embedded)")"
PRECONNECT_ECHO_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-preconnect-echo)")"
CRED_SHELL_ECHO_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-cred-shell-echo)")"
ELICITATION_ECHO_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-elicitation-echo)")"
ECHO_CONCURRENT_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-echo-concurrent)")"
HEARTBEAT_SILENCE_ECHO_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-heartbeat-silence-echo)")"
HEARTBEAT_SILENCE_ECHO_CONCURRENT_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-heartbeat-silence-echo-concurrent)")"
SUPERVISE_PROBE_IMAGE="$(resolve_digest "$(image_ref lenny-runtime-supervise-probe)")"
log "echo runtime image pinned to ${ECHO_IMAGE}"

# ---------------------------------------------------------------------
# Step 2c: register the digest-pinned reference in each node's containerd.
#
# The agent pods (and the §17.6 bootstrap runtimes) reference each runtime
# image by its digest-pinned form `<repo>:<tag>@sha256:<id>` because the
# §13.1 admission plane rejects a bare tag. `kind load docker-image`
# imports the image into containerd under its tag name only, so containerd
# has `lenny-runtime-echo:<tag>` but no image named
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
  "${ECHO_CONCURRENT_IMAGE}"
  "${HEARTBEAT_SILENCE_ECHO_IMAGE}"
  "${HEARTBEAT_SILENCE_ECHO_CONCURRENT_IMAGE}"
  "${SUPERVISE_PROBE_IMAGE}"
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

# §12.5 — the artifact-store TLS mandate binds the plaintext-MinIO
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

# §13.2 NET-071 — agent pods trust the same MinIO CA during the checkpoint
# chunk PUT/GET. The gateway and the bucket-lifecycle Job read the CA from the
# lenny-system ConfigMap named by minio.tls.caBundleConfigMap, but the chart
# cannot copy a ConfigMap's bytes into an agent namespace from a name alone, so
# the agent-pod path is gated on minio.tls.caBundle carrying the PEM directly
# (charts/lenny/templates/agent-objectstore-ca.yaml materializes it per agent
# namespace and controller-deployment.yaml renders --objectstore-ca-configmap
# under the same condition). Write the issued CA to a file and pass it to helm
# as minio.tls.caBundle; without it the adapter cannot complete the TLS
# handshake to the self-managed MinIO and every checkpoint upload fails closed.
MINIO_CA_FILE="${REPO_ROOT}/tests/testinfra/kind/minio-ca.gen.crt"
printf '%s\n' "${MINIO_CA_CRT}" >"${MINIO_CA_FILE}"

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
# The manifest names lenny-migrate:e2e; substitute the reference this
# install deploys.
sed "s|image: lenny-migrate:e2e|image: $(image_ref lenny-migrate)|" "${MIGRATE_JOB_MANIFEST}" | kc apply -f -
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
# MinIO serves https on :9000 (§12.5; datastores.yaml issues the
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
# to the lenny-runtime-* images this install deployed (resolved at install
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
    # §5.3 sandboxed-profile reference runtime. Backs gvisor-echo-pool,
    # the only pool on this overlay that runs under the gvisor
    # RuntimeClass rather than runc. install-gvisor.sh installs runsc on
    # the sandbox-gvisor labelled node and applies the RuntimeClass
    # before this pool's pod is scheduled (Step 3a, ahead of this
    # Step-9 apply). Uses the same distroless echo image as
    # echo-runtime-sidecar: gvisor_isolation_test.go fingerprints the
    # gVisor sentry kernel through a short-lived ephemeral debug
    # container (busybox) rather than execing into this shell-less
    # image directly. spec: §5.3.
    - name: gvisor-echo-runtime
      type: agent
      image: ${ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: sandboxed
      labels:
        lenny.dev/e2e: gvisor-echo
    # §5.2 sessionId dispatch-loop reference runtime, the sidecar-model
    # counterpart of the tier-4 concurrent_workspace_test.go in-process
    # flow. Backs concurrent-echo-pool, whose sessionPolicy.
    # maxConcurrentSessions > 1 multiplexes simultaneous sessions onto
    # this runtime's single stdin/stdout dispatch loop keyed on sessionId.
    # capabilities.injection.supported is true (§5.1) to match the Runtime
    # CRD in agent-workload.yaml: each slot's echocore loop answers every
    # inbound `message` frame, so the tier-5 harness can send several
    # messages on each slot session.
    - name: echo-runtime-concurrent
      type: agent
      image: ${ECHO_CONCURRENT_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: echo-concurrent
    # §5.2 sequential-pod-reuse ("task mode") reference runtime. Reuses
    # ECHO_IMAGE under a distinct name so task-mode-echo-pool has a
    # runtimeRef no other pool shares (see agent-workload.yaml for the
    # ErrAmbiguousPool rationale).
    - name: echo-runtime-task-mode
      type: agent
      image: ${ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      labels:
        lenny.dev/e2e: echo-task-mode
    # Test-only heartbeat-silence fixture runtimes (see IMAGE_SPECS and
    # agent-workload.yaml). They back only the heartbeat-silence pools
    # below. capabilities.injection.supported is true (§5.1) to match the
    # Runtime CRDs: the wrapped echo loops answer every inbound `message`
    # frame, so a session can carry several messages.
    - name: heartbeat-silence-echo-runtime
      type: agent
      image: ${HEARTBEAT_SILENCE_ECHO_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: heartbeat-silence-echo
    - name: heartbeat-silence-concurrent-runtime
      type: agent
      image: ${HEARTBEAT_SILENCE_ECHO_CONCURRENT_IMAGE}
      integrationLevel: basic
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: heartbeat-silence-concurrent
    # Test-only supervise-probe runtimes (tests/testinfra/superviseprobe) for
    # the runtime supervisor's cluster tiers. Each backs one supervised probe
    # pool below, so each pool is the only match for its runtimeRef. The
    # matching Runtime CRs in agent-workload.yaml carry the command and env
    # the supervisor launches with (§5.1 command and env), which the pod
    # builder reads from the CRD. They are Standard level, so the probe's
    # manifest names the platform MCP server. capabilities.injection is
    # supported so a test can send several commands on one session.
    - name: supervise-probe-keep
      type: agent
      image: ${SUPERVISE_PROBE_IMAGE}
      integrationLevel: standard
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      # Setup commands run in the adapter container, whose distroless image
      # has no /bin/sh, so this runtime execs them as argv (§7.5). The
      # setup-residue case runs the probe binary the e2e adapter image
      # carries (the lenny-adapter entry in IMAGE_SPECS).
      setupCommandPolicy:
        shell: false
      labels:
        lenny.dev/e2e: supervise-probe-keep
    - name: supervise-probe-idle
      type: agent
      image: ${SUPERVISE_PROBE_IMAGE}
      integrationLevel: standard
      executionMode: session
      isolationProfile: standard
      capabilities:
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: supervise-probe-idle
    - name: supervise-probe-preconnect
      type: agent
      image: ${SUPERVISE_PROBE_IMAGE}
      integrationLevel: standard
      executionMode: session
      isolationProfile: standard
      capabilities:
        preConnect: true
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: supervise-probe-preconnect
    - name: supervise-probe-podwarm
      type: agent
      image: ${SUPERVISE_PROBE_IMAGE}
      integrationLevel: standard
      executionMode: session
      isolationProfile: standard
      capabilities:
        preConnect: true
        interaction: multi_turn
        injection:
          supported: true
          modes: [immediate, queued]
      labels:
        lenny.dev/e2e: supervise-probe-podwarm
    - name: supervise-probe-service
      type: agent
      image: ${SUPERVISE_PROBE_IMAGE}
      integrationLevel: standard
      executionMode: service
      isolationProfile: standard
      labels:
        lenny.dev/e2e: supervise-probe-service
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
    # §5.3 sandboxed isolation profile is the production default and
    # needs no allowStandardIsolation-style opt-in flag. No dnsPolicy
    # override: §13.2 rejects dnsPolicy: cluster-default on a
    # non-standard-isolation pool (the dedicated-CoreDNS opt-out is
    # standard-only), and none is needed here — coredns.clusterIP is
    # unset on this overlay, so the Sandbox reconciler's applyDedicatedDNS
    # already falls back every agent pod (regardless of pool) to the
    # default ClusterFirst resolver. warmCount: 1 — gvisor_isolation_
    # test.go is the only consumer and claims no pod (it inspects the
    # warm, unclaimed pod directly), so a single replica is enough
    # headroom.
    - name: gvisor-echo-pool
      runtimeRef: gvisor-echo-runtime
      isolationProfile: sandboxed
      executionMode: session
      warmCount: 1
    # §5.2 sequential pod-reuse ("task mode") pool: recycle.enabled with
    # maxConcurrentSessions left at the default of 1 serves successive
    # sessions on the one warm pod, with a whole-pod Lenny scrub between
    # them. warmCount: 2, not 1 — claiming the sole idle pod on a
    # warmCount: 1 pool through the combined POST /v1/sessions/start path
    # trips the pool's own PoolWarmingUp bootstrap condition (idle count
    # drops to 0 the instant the claim lands, before the replacement
    # warms), and startOnPod's post-claim ResolvePool re-check then aborts
    # the just-claimed pod; this reproduces on any pre-existing warmCount:
    # 1 pool (verified directly against gvisor-echo-pool) and is unrelated
    # to sessionPolicy — tracked separately, not a task-mode-specific
    # defect. A depth of 2 keeps a spare idle pod so
    # tests/tier5_e2e_kind/execution_modes_test.go's claims do not race
    # it; tenant pinning plus the reserved-pod preference (§5.2 "the pod
    # is held for its tenant through the claim's reserved state and
    # serves the next session") still routes the tenant's second session
    # back onto the same recycled pod. maxSessionsPerPod is required with
    # no default when recycle.enabled is true (§5.2); 5 gives the test
    # headroom for more than one cycle. maxSessionsPerPod above 1 with a
    # non-vm-restart scrub profile makes this a reusing recycling pool, so
    # §5.2 requires recycle.runtimeProcess. The pool sets keep, so the pod
    # keeps its runtime process across the sessions it serves. That keep
    # setting is why §5.2 requires
    # sessionPolicy.acknowledgeProcessLevelIsolation: true and why pool
    # admission refuses the bootstrap seed without it.
    - name: task-mode-echo-pool
      runtimeRef: echo-runtime-task-mode
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        acknowledgeProcessLevelIsolation: true
        recycle:
          enabled: true
          acknowledgeBestEffortScrub: true
          maxSessionsPerPod: 5
          runtimeProcess: keep
        cleanupTimeoutSeconds: 30
    # §5.2 concurrent-session pool: maxConcurrentSessions > 1 multiplexes
    # simultaneous sessions onto one warm pod's echo-concurrent process,
    # each in its own sessionId-addressed slot. warmCount: 2 for the same
    # PoolWarmingUp-race reason as task-mode-echo-pool above,
    # deliberately above 1 even though a single pod's 2 slots are enough
    # capacity for the test's two sessions. cleanupTimeoutSeconds must be
    # >= maxConcurrentSessions * 5 (§5.2 per-slot cleanup timeout floor);
    # 30 clears the 2 * 5 = 10s minimum with headroom.
    - name: concurrent-echo-pool
      runtimeRef: echo-runtime-concurrent
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        maxConcurrentSessions: 2
        acknowledgeProcessLevelIsolation: true
        cleanupTimeoutSeconds: 30
    # §5.2 recycling pool with maxConcurrentSessions left at the default of
    # 1, on the heartbeat-silence fixture. It mirrors task-mode-echo-pool so
    # the tier-5 stream-failure case can show that a pod whose runtime
    # stopped answering heartbeats is retired rather than recycled. warmCount
    # is 2 for the PoolWarmingUp-race reason task-mode-echo-pool states.
    - name: heartbeat-silence-recycle-pool
      runtimeRef: heartbeat-silence-echo-runtime
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        acknowledgeProcessLevelIsolation: true
        recycle:
          enabled: true
          acknowledgeBestEffortScrub: true
          maxSessionsPerPod: 5
          runtimeProcess: keep
        cleanupTimeoutSeconds: 30
    # §5.2 concurrent-session pool on the heartbeat-silence fixture. It
    # mirrors concurrent-echo-pool so the tier-5 stream-failure case can
    # count failed slots toward the whole-pod replacement trigger.
    - name: heartbeat-silence-concurrent-pool
      runtimeRef: heartbeat-silence-concurrent-runtime
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        maxConcurrentSessions: 2
        acknowledgeProcessLevelIsolation: true
        cleanupTimeoutSeconds: 30
    # Supervised probe pools for the runtime supervisor's cluster tiers
    # (tests/tier5_e2e_kind/runtime_supervisor_test.go and the tier-8, tier-9,
    # and tier-10 cases). warmCount is 2 for the PoolWarmingUp-race reason
    # task-mode-echo-pool states.
    #
    # supervise-probe-keep-pool is a reusing recycling pool under
    # recycle.runtimeProcess: keep, so its pods keep one runtime process across
    # the sessions they serve and §5.2 requires
    # acknowledgeProcessLevelIsolation. Its cleanupCommands run the probe's
    # cleanup-record subcommand, which the supervisor runs at the agent UID in
    # the runtime container (§4.7.10 Runtime supervisor, Cleanup) and which
    # reports to the kept runtime process.
    #
    # The matching restart pool is not seeded: gateway pool admission refuses
    # recycle.runtimeProcess: restart until proposal 0084 rebinds the
    # intra-pod MCP servers per runtime generation, so the seed would fail the
    # bootstrap Job. The restart cases skip with a blocked reason naming that
    # refusal, and proposal 0084 adds the pool when it removes the refusal.
    # The cases are written against supervise-probe-restart-pool on the
    # supervise-probe-restart Runtime, a copy of the keep pool under
    # runtimeProcess: restart whose cleanupCommands run the probe's
    # cleanup-record and daemon-child subcommands, so a cleanup command
    # leaves a background process.
    - name: supervise-probe-keep-pool
      runtimeRef: supervise-probe-keep
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        acknowledgeProcessLevelIsolation: true
        recycle:
          enabled: true
          acknowledgeBestEffortScrub: true
          maxSessionsPerPod: 5
          runtimeProcess: keep
        cleanupCommands:
          - /usr/local/bin/lenny cleanup-record
        cleanupTimeoutSeconds: 30
    # supervise-probe-idle-pool is a second keep pool on which a tier-8 case
    # returns a used pod to idle, pinned to the tenant that used it, kills
    # its kept runtime process, and makes that tenant's next start reach it
    # (§4.7.10 Runtime supervisor, Liveness, the idle row's Start channel).
    # The pool store sets minWarm and maxWarm both from warmCount, so the
    # pool keeps no pinned idle pod (§5.2 Pinned idle inventory) and the
    # case skips, blocked, until a pool can set maxWarm above minWarm.
    - name: supervise-probe-idle-pool
      runtimeRef: supervise-probe-idle
      isolationProfile: standard
      executionMode: session
      warmCount: 1
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sessionPolicy:
        acknowledgeProcessLevelIsolation: true
        recycle:
          enabled: true
          acknowledgeBestEffortScrub: true
          maxSessionsPerPod: 5
          runtimeProcess: keep
        cleanupTimeoutSeconds: 30
    # Sidecar preConnect probe pool: its pods launch the runtime process at
    # warm-up (--launch-mode=preconnect-sdk-warm) while the pool's SDK-warm
    # circuit breaker is closed. The Runtime CR sets the CH-MSGSOCK dial
    # delay (PROBE_DIAL_DELAY) of the warm-up launch between the rendered
    # ping interval plus ping timeout and the warm-up timeout; a step-7
    # launch after a demotion dials at once (§4.7.10 Runtime supervisor,
    # Launch; SDK-warm; §28.5.3 CH-SUPERVISE, Timing).
    - name: supervise-probe-preconnect-pool
      runtimeRef: supervise-probe-preconnect
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
    # Sidecar preConnect probe pool created with the SDK-warm circuit breaker
    # forced open, so its pods are created under
    # --launch-mode=preconnect-pod-warm and launch at startup step 7 (§6.1
    # Circuit-breaker for SDK-warm pools). It warms two pods, as the
    # preConnect pool does, so a session start that claims one leaves an
    # idle pod and the pool does not report PoolWarmingUp while the start
    # launches on the claimed pod (§5.2 PoolWarmingUp condition).
    - name: supervise-probe-podwarm-pool
      runtimeRef: supervise-probe-podwarm
      isolationProfile: standard
      executionMode: session
      warmCount: 2
      allowStandardIsolation: true
      dnsPolicy: cluster-default
      sdkWarm:
        circuitBreakerOverride: disabled
    # Supervised service-mode probe pool: the runtime process is launched at
    # warm-up and kept for the pod's life (§4.7.10 Runtime supervisor,
    # Launch). maxConcurrent is the service-mode per-pod capacity §5.2
    # requires. The Runtime CR sets the same dial delay as the preConnect pool.
    - name: supervise-probe-service-pool
      runtimeRef: supervise-probe-service
      isolationProfile: standard
      executionMode: service
      maxConcurrent: 1
      warmCount: 1
      allowStandardIsolation: true
      dnsPolicy: cluster-default
EOF

# §25.4 — "No anonymous access except /healthz." In dev mode
# (global.devMode renders --dev-mode on lenny-ops) lenny-ops only wires
# its authentication gate when a bearer-trust key is configured; without
# it cmd/lenny-ops/main.go buildAuthConfig returns a nil AuthConfig and
# the operability surface admits anonymous callers. Create the bearer-trust key Secret named by
# security.oidc.bearerTrustKeySecret in e2e-values.yaml so the deployed
# lenny-ops runs with a RequireAuth AuthConfig and the §25.4 auth gate is
# exercised end-to-end. The Secret holds a §17.4 HMAC key file (keyId +
# base64 secret) under data key `key`, the on-disk format
# jwt.LoadHMACKeyFile parses; the chart mounts it at /etc/lenny/oidc/key
# and passes --bearer-trust-hmac-key-file. The key is a fixed dev value:
# the e2e callers authenticate through the dev-mode identity headers
# (global.devMode: true renders --dev-mode on lenny-ops), so no test mints a bearer
# against this key. It exists only so the auth gate is wired and anonymous
# callers are rejected. create --dry-run | apply keeps the step
# idempotent across re-runs.
log "creating the lenny-ops bearer-trust key Secret"
kc -n lenny-system create secret generic lenny-e2e-bearer-trust-key \
  --from-literal=key='{"keyId":"lenny-e2e-bearer-trust","secret":"bGVubnktZTJlLWJlYXJlci10cnVzdC1obWFjLWtleS0zMmJ5dGVz"}' \
  --dry-run=client -o yaml | kc apply -f -

# Generate the image-tag values overlay. e2e-values.yaml names every
# platform image with the e2e tag; this overlay replaces each with the tag
# this install deploys, so a rebuilt image changes the rendered image
# reference and its Deployment rolls out. It is passed after
# e2e-values.yaml and before the bootstrap overlay.
IMAGE_TAGS_OVERLAY="${REPO_ROOT}/tests/testinfra/kind/image-tags.gen.yaml"
log "generating the image-tag values overlay"
cat >"${IMAGE_TAGS_OVERLAY}" <<EOF
# Generated by install.sh; do not edit. Regenerate by re-running install.sh.
controller:
  image:
    tag: $(image_tag lenny-controller)
  adapterImage: $(image_ref lenny-adapter)
  supervisorImage: $(image_ref lenny-supervisor)
  egressCaptureImage: $(image_ref lenny-egress-capture)
admissionWebhooks:
  image:
    tag: $(image_tag lenny-webhook)
tokenService:
  image:
    tag: $(image_tag lenny-token-service)
gateway:
  image:
    tag: $(image_tag lenny-gateway)
ops:
  image:
    tag: $(image_tag lenny-ops)
migrate:
  image:
    tag: $(image_tag lenny-migrate)
preflight:
  image:
    tag: $(image_tag lenny-preflight)
backups:
  image:
    tag: $(image_tag lenny-backup)
poolScalingController:
  image:
    tag: $(image_tag lenny-pool-scaling-controller)
bootstrap:
  image:
    tag: $(image_tag lenny-ctl)
EOF

# --server-side=false forces client-side apply. Helm 4 defaults to
# server-side apply, whose strict (containerPort, protocol) list-map key
# rejects the chart's named http+metrics ports sharing one containerPort
# (§16.9); helm 3 used client-side apply and tolerated it. The
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
    -f "${IMAGE_TAGS_OVERLAY}" \
    -f "${BOOTSTRAP_OVERLAY}" \
    --set-file minio.tls.caBundle="${MINIO_CA_FILE}" \
    --server-side=false \
    --timeout 420s
else
  log "installing the Lenny Helm chart"
  helm install lenny "${CHART_DIR}" \
    -n lenny-system \
    --kube-context "${KCTX}" \
    -f "${E2E_VALUES}" \
    -f "${IMAGE_TAGS_OVERLAY}" \
    -f "${BOOTSTRAP_OVERLAY}" \
    --set-file minio.tls.caBundle="${MINIO_CA_FILE}" \
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
#
# A helm upgrade that changes an image tag rolls the Deployment out, and
# `kubectl wait pod -l` resolves the selector once, so it would wait on the
# replaced pods, which terminate rather than become Ready. Each Deployment's
# rollout is awaited first so the pod wait sees only the current pods.
log "waiting for the lenny-system control-plane rollouts to complete"
for deploy in $(kc -n lenny-system get deploy -l app.kubernetes.io/name=lenny -o name); do
  kc -n lenny-system rollout status "${deploy}" --timeout=300s
done
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
# before applying; the §13.1 admission plane rejects a bare tag, so
# every image must be the pinned image:<tag>@sha256:<digest> form.
# ---------------------------------------------------------------------
log "applying the agent Runtime CRs (digest-pinned)"
sed \
  -e "s|__ECHO_IMAGE__|${ECHO_IMAGE}|g" \
  -e "s|__ECHO_EMBEDDED_IMAGE__|${ECHO_EMBEDDED_IMAGE}|g" \
  -e "s|__PRECONNECT_ECHO_IMAGE__|${PRECONNECT_ECHO_IMAGE}|g" \
  -e "s|__CRED_SHELL_ECHO_IMAGE__|${CRED_SHELL_ECHO_IMAGE}|g" \
  -e "s|__ELICITATION_ECHO_IMAGE__|${ELICITATION_ECHO_IMAGE}|g" \
  -e "s|__ECHO_CONCURRENT_IMAGE__|${ECHO_CONCURRENT_IMAGE}|g" \
  -e "s|__HEARTBEAT_SILENCE_ECHO_IMAGE__|${HEARTBEAT_SILENCE_ECHO_IMAGE}|g" \
  -e "s|__HEARTBEAT_SILENCE_ECHO_CONCURRENT_IMAGE__|${HEARTBEAT_SILENCE_ECHO_CONCURRENT_IMAGE}|g" \
  -e "s|__SUPERVISE_PROBE_IMAGE__|${SUPERVISE_PROBE_IMAGE}|g" \
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
