#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/run-e2e.sh — end-to-end driver for a tier-6 GKE
# verification run. Mirrors scripts/cloud/aws/run-e2e.sh with the GCP
# equivalents: Cloud KMS, GCS, Workload Identity, and Artifact
# Registry (in lieu of ECR).
#
# Sequence:
#
#   1. scripts/cloud/gcp/up.sh        — terraform apply (KMS + GCS +
#                                       Workload Identity binding).
#   2. docker push images to Artifact Registry (operator-supplied).
#   3. helm template lenny chart with the GCP values overlay.
#   4. kubectl apply -f tests/testinfra/k8s/datastores.yaml
#                                     — Postgres + Redis fixtures
#                                       inside the cluster (GCS replaces
#                                       MinIO).
#   5. helm install lenny-e2e         — chart install with the GCP
#                                       overlay; wait for ready.
#   6. lenny-test --tier e2e_cloud    — run the tier-6 suite.
#   7. scripts/cloud/gcp/down.sh      — terraform destroy on exit
#                                       (unless KEEP_CLUSTER=1).
#
# Required environment:
#
#   GCP_PROJECT                  — the project the bucket / key live in.
#   GCP_REGION                   — default us-central1.
#   GKE_CLUSTER_NAME             — operator-supplied GKE cluster.
#   GKE_CLUSTER_LOCATION         — cluster zone or region.
#
# Optional environment:
#
#   LENNY_RELEASE                — Helm release name + Terraform prefix.
#                                  Default lenny-e2e.
#   IMAGE_TAG                    — image tag (default 0.1.0).
#   KEEP_CLUSTER=1               — default. Skip terraform destroy on exit.
#   LENNY_NAMESPACE              — chart namespace. Default lenny-system.
#
# This driver is intentionally a higher-level orchestrator than
# scripts/cloud/aws/run-e2e.sh — many of the AWS-side managed-service
# add-ons (RDS, ElastiCache) map onto GCP equivalents (Cloud SQL,
# Memorystore) that are not yet covered by the GCP Terraform module.
# The driver runs cleanly against the existing Terraform skeleton and
# leaves room for future managed-service additions.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-e2e}"
IMAGE_TAG="${IMAGE_TAG:-0.1.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"
LENNY_NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"

GCP_PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"
GCP_REGION="${GCP_REGION:-us-central1}"
GKE_CLUSTER_NAME="${GKE_CLUSTER_NAME:?GKE_CLUSTER_NAME must be set}"
GKE_CLUSTER_LOCATION="${GKE_CLUSTER_LOCATION:-${GCP_REGION}}"

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"

log() { printf '==> %s\n' "$*"; }

trap_exit() {
  if [[ "${KEEP_CLUSTER}" != "1" ]]; then
    log "tearing down GCP resources (KEEP_CLUSTER!=1)"
    "${REPO_ROOT}/scripts/cloud/gcp/down.sh" || true
  fi
}
trap trap_exit EXIT

log "step 1: scripts/cloud/gcp/up.sh"
ENV_OUT=$(GCP_PROJECT="${GCP_PROJECT}" GCP_REGION="${GCP_REGION}" \
  GKE_CLUSTER_NAME="${GKE_CLUSTER_NAME}" \
  GKE_CLUSTER_LOCATION="${GKE_CLUSTER_LOCATION}" \
  LENNY_NAMESPACE="${LENNY_NAMESPACE}" \
  "${REPO_ROOT}/scripts/cloud/gcp/up.sh" "${LENNY_RELEASE}")
eval "${ENV_OUT}"

log "step 2: Push Lenny images to Artifact Registry (operator-supplied)"
log "  set DOCKER_REGISTRY=<region>-docker.pkg.dev/<project>/<repo>"
log "  and run scripts/cloud/aws/build-images.sh equivalents."
log "  (this step is left as an operator action so the script does not"
log "   assume a specific registry layout)"

log "step 3: render Helm values overlay"
cat > "${REPO_ROOT}/scripts/cloud/gcp/values-cloud-gcp.${LENNY_RELEASE}.yaml" <<EOF
controller:
  egressCaptureImage: ""
gateway:
  serviceAccount:
    annotations:
      iam.gke.io/gcp-service-account: ${LENNY_GCP_SERVICE_ACCOUNT_EMAIL}
features:
  llmProxy: true
EOF

log "step 4: in-cluster Postgres + Redis fixtures (or Cloud SQL / Memorystore)"
kubectl apply -f "${REPO_ROOT}/tests/testinfra/k8s/datastores.yaml"

log "step 4b: RuntimeClasses (runc + gvisor + kata-containers)"
# The Sandbox controller maps §5.3 isolationProfile to a Kubernetes
# RuntimeClass on the pod spec: standard → runc, sandboxed → gvisor,
# microvm → kata-containers. Kubernetes admission rejects a pod
# whose runtimeClassName references a missing RuntimeClass, so all
# three resources must exist on the cluster even when the underlying
# node-level runtime is not installed. Same pattern as the AWS
# run-e2e.sh step 5c.
kubectl apply -f - <<'RUNTIMECLASSES'
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: runc
  labels:
    app.kubernetes.io/name: lenny
    lenny.dev/component: runtime-class
handler: runc
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
  labels:
    app.kubernetes.io/name: lenny
    lenny.dev/component: runtime-class
handler: runsc
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata-containers
  labels:
    app.kubernetes.io/name: lenny
    lenny.dev/component: runtime-class
handler: kata
RUNTIMECLASSES

log "step 5: helm install ${LENNY_RELEASE}"
helm upgrade --install "${LENNY_RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace "${LENNY_NAMESPACE}" --create-namespace \
  --values "${REPO_ROOT}/scripts/cloud/gcp/values-cloud-gcp.${LENNY_RELEASE}.yaml" \
  --wait --timeout 10m

log "step 6: run tier-6 suite"
cd "${REPO_ROOT}"
LENNY_CLOUD_PROVIDER=gcp \
LENNY_GCP_PROJECT="${LENNY_GCP_PROJECT}" \
LENNY_GCP_REGION="${LENNY_GCP_REGION}" \
LENNY_GCP_KMS_KEY_ID="${LENNY_GCP_KMS_KEY_ID}" \
LENNY_GCP_ARTIFACT_BUCKET="${LENNY_GCP_ARTIFACT_BUCKET}" \
lenny-test --tier e2e_cloud --output human

log "tier-6 GKE run complete; KEEP_CLUSTER=${KEEP_CLUSTER}"
