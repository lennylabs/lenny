#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/eks/run-e2e.sh — single end-to-end driver for a
# tier-6 EKS verification run.
#
# Sequence:
#
#   1. scripts/cloud/eks/up.sh        — terraform apply (VPC + EKS +
#                                       S3 + KMS + IRSA).
#   2. scripts/cloud/eks/build-images.sh
#                                     — build + push Lenny images
#                                       to ECR.
#   3. scripts/cloud/eks/render-values.sh
#                                     — render a cloud values overlay
#                                       pointing the chart at ECR +
#                                       the Terraform outputs.
#   4. kubectl apply -f tests/testinfra/kind/datastores.yaml
#                                     — Postgres + Redis + MinIO
#                                       fixtures inside the cluster.
#   5. helm install lenny-e2e         — chart install with the cloud
#                                       overlay; wait for ready.
#   6. lenny-test --tier e2e_cloud    — run the tier-6 suite against
#                                       the freshly-installed gateway.
#   7. scripts/cloud/eks/down.sh      — terraform destroy on exit
#                                       (unless KEEP_CLUSTER=1).
#
# The script is idempotent at each step: re-running after a failure
# resumes from the next step. `KEEP_CLUSTER=1` leaves the cluster
# running on exit so the operator can poke at the install.
#
# Required environment:
#
#   AWS_PROFILE                  — the SSO profile (e.g. lenny-tier6).
#   AWS_REGION                   — target region (default us-west-2).
#
# Optional environment:
#
#   LENNY_RELEASE                — Helm release name + Terraform prefix.
#                                  Default lenny-e2e.
#   IMAGE_TAG                    — image tag (default 0.1.0).
#   SHAPE                        — up.sh shape (default cloud-small).
#   KEEP_CLUSTER=1               — skip the terraform destroy on exit.

set -euo pipefail

SHAPE="${SHAPE:-cloud-small}"
REGION="${AWS_REGION:-us-west-2}"
RELEASE="${LENNY_RELEASE:-lenny-e2e}"
TAG="${IMAGE_TAG:-0.1.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="${REPO_ROOT}/scripts/cloud/eks"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
VALUES_OUT="${REPO_ROOT}/scripts/cloud/eks/values-cloud-aws.${RELEASE}.yaml"

# Pick terraform / tofu the same way the sub-scripts do.
if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "run-e2e.sh: neither terraform nor tofu on PATH" >&2
  exit 3
fi

# Required CLIs.
for cli in aws kubectl helm docker go; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "run-e2e.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done

# Verify AWS credentials are valid before doing any expensive work.
if ! aws sts get-caller-identity >/dev/null 2>&1; then
  echo "run-e2e.sh: aws sts get-caller-identity failed; run 'aws sso login --profile ${AWS_PROFILE:-<profile>}'" >&2
  exit 3
fi

cleanup() {
  local rc=$?
  if [[ "${KEEP_CLUSTER}" == "1" ]]; then
    echo "run-e2e.sh: KEEP_CLUSTER=1, leaving the cluster running" >&2
    exit "${rc}"
  fi
  echo "run-e2e.sh: tearing down the cluster (exit code ${rc})" >&2
  AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
    "${SCRIPT_DIR}/down.sh" || true
  exit "${rc}"
}
trap cleanup EXIT

# 1. terraform apply (VPC + EKS + S3 + KMS + IRSA).
echo "==[1/6] terraform apply (shape=${SHAPE})==" >&2
AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
  "${SCRIPT_DIR}/up.sh" "${SHAPE}"

# 2. Build + push Lenny images to ECR.
echo "==[2/6] build + push images (tag=${TAG})==" >&2
AWS_REGION="${REGION}" IMAGE_TAG="${TAG}" \
  "${SCRIPT_DIR}/build-images.sh"

# Capture the ECR registry host the build script printed; re-derive
# from AWS for safety against a stdout-capture mistake.
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
ECR_REGISTRY="${ACCOUNT}.dkr.ecr.${REGION}.amazonaws.com"
export ECR_REGISTRY IMAGE_TAG="${TAG}"

# 3. Render the cloud values overlay.
echo "==[3/6] render cloud values overlay==" >&2
ECR_REGISTRY="${ECR_REGISTRY}" IMAGE_TAG="${TAG}" \
  "${SCRIPT_DIR}/render-values.sh" "${VALUES_OUT}"

# 4. Prerequisites + in-cluster datastores. The chart's templates
#    render cert-manager Certificate / Issuer resources and
#    prometheus-operator PrometheusRule resources; those CRDs must
#    exist on the cluster before `helm install` parses the manifest.
#    Apply them out of band, matching what
#    tests/testinfra/kind/install.sh does on Kind. The datastore
#    fixtures follow.
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"
PROM_OPERATOR_VERSION="${PROM_OPERATOR_VERSION:-v0.79.2}"
echo "==[4/6] install cert-manager ${CERT_MANAGER_VERSION} + prometheus-operator CRDs + datastores==" >&2

if ! kubectl -n cert-manager get deploy cert-manager >/dev/null 2>&1; then
  echo "  installing cert-manager ${CERT_MANAGER_VERSION}" >&2
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
fi
kubectl -n cert-manager wait --for=condition=Available deploy --all --timeout=300s

PROM_CRD_BASE="https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/${PROM_OPERATOR_VERSION}/example/prometheus-operator-crd"
for crd in prometheusrules servicemonitors podmonitors; do
  if ! kubectl get crd "${crd}.monitoring.coreos.com" >/dev/null 2>&1; then
    echo "  installing prometheus-operator CRD ${crd}" >&2
    kubectl apply --server-side -f "${PROM_CRD_BASE}/monitoring.coreos.com_${crd}.yaml"
  fi
done

# The monitoring namespace the chart's PrometheusRule lands in must
# exist before the apply.
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -

kubectl create namespace lenny-system --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n lenny-system -f "${REPO_ROOT}/tests/testinfra/kind/datastores.yaml"
kubectl wait -n lenny-system --for=condition=available --timeout=300s \
  deployment/lenny-postgres deployment/lenny-redis deployment/lenny-minio

# Apply the chart's CRDs out of band. helm install installs them on
# first invoke; this step keeps a re-run idempotent.
echo "==[4b/6] apply Lenny CRDs==" >&2
kubectl apply -f "${REPO_ROOT}/charts/lenny/crds/"

# Pre-pull the Lenny images to every node in parallel via a tiny
# DaemonSet. ECR cold-pull on a freshly provisioned EKS node is
# 60-120s per image, and the migrate Job + helm install would
# otherwise hit each image's first pull serially. Running the pulls
# in parallel up front shaves multiple minutes off the rest of the
# cycle and surfaces image-pull failures (private registry auth,
# image-not-found) at a single observable point.
echo "==[4d/6] pre-pull Lenny images on every node==" >&2
cat <<PREPULL | kubectl apply -f -
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: lenny-e2e-image-prepull
  namespace: lenny-system
spec:
  selector:
    matchLabels: {app: lenny-e2e-image-prepull}
  template:
    metadata:
      labels: {app: lenny-e2e-image-prepull}
    spec:
      restartPolicy: Always
      terminationGracePeriodSeconds: 1
      initContainers:
        - name: pull-gateway
          image: ${ECR_REGISTRY}/lenny-gateway:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-controller
          image: ${ECR_REGISTRY}/lenny-controller:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-token-service
          image: ${ECR_REGISTRY}/lenny-token-service:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-ops
          image: ${ECR_REGISTRY}/lenny-ops:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-webhook
          image: ${ECR_REGISTRY}/lenny-webhook:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-preflight
          image: ${ECR_REGISTRY}/lenny-preflight:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-backup
          image: ${ECR_REGISTRY}/lenny-backup:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-ctl
          image: ${ECR_REGISTRY}/lenny-ctl:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-adapter
          image: ${ECR_REGISTRY}/lenny-adapter:${TAG}
          command: ["/bin/sh","-c","true"]
        - name: pull-migrate
          image: ${ECR_REGISTRY}/lenny-migrate:${TAG}
          command: ["/bin/sh","-c","true"]
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
PREPULL
# Distroless's pause image is also handy here; the init containers
# do the actual pulls.
kubectl -n lenny-system rollout status daemonset/lenny-e2e-image-prepull --timeout=600s

# Run the schema migration against the in-cluster Postgres before
# helm install brings the gateway / controller up. The Kind path uses
# tests/testinfra/kind/migrate-job.yaml with a kind-loaded image; on
# EKS we render an equivalent Job that pulls the lenny-migrate image
# from ECR.
echo "==[4c/6] run lenny-migrate Job==" >&2
kubectl -n lenny-system delete job lenny-e2e-migrate --ignore-not-found --wait=true >/dev/null
cat <<MIGRATE | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: lenny-e2e-migrate
  namespace: lenny-system
spec:
  backoffLimit: 3
  ttlSecondsAfterFinished: 600
  template:
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
      containers:
        - name: migrate
          image: ${ECR_REGISTRY}/lenny-migrate:${TAG}
          imagePullPolicy: IfNotPresent
          args: ["up"]
          env:
            - name: LENNY_POSTGRES_DSN
              value: "postgres://lenny:lenny@lenny-postgres.lenny-system.svc:5432/lenny?sslmode=disable"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
MIGRATE
# Bumped to 600s: ECR cold-pull on a freshly provisioned EKS node
# is materially slower than Kind's pre-loaded image cache. The first
# pull of each Lenny image on a node typically takes 60-120s; a
# concurrent first-pull plus pod scheduling can spend most of the
# previous 300s window before the migrate command even runs.
if ! kubectl -n lenny-system wait --for=condition=complete job/lenny-e2e-migrate --timeout=600s; then
  echo "==[4c/6] migrate Job did not complete; capturing diagnostics==" >&2
  kubectl -n lenny-system describe job/lenny-e2e-migrate >&2 || true
  for pod in $(kubectl -n lenny-system get pods -l job-name=lenny-e2e-migrate -o name 2>/dev/null); do
    echo "--- ${pod} describe ---" >&2
    kubectl -n lenny-system describe "${pod}" >&2 || true
    echo "--- ${pod} logs ---" >&2
    kubectl -n lenny-system logs "${pod}" --tail=200 >&2 || true
  done
  echo "--- recent lenny-system events ---" >&2
  kubectl -n lenny-system get events --sort-by=.lastTimestamp 2>&1 | tail -30 >&2 || true
  exit 1
fi

# 5. Helm install (the chart enforces a few install-time invariants;
#    --skip-tests skips the operator's chart-test hooks which assume
#    out-of-cluster Postgres / Redis).
echo "==[5/6] helm install lenny ${RELEASE}==" >&2
helm upgrade --install "${RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace lenny-system \
  -f "${VALUES_OUT}" \
  --set gateway.noEnvironmentPolicy=allow-all \
  --wait \
  --timeout 10m

# 6. Run the tier-6 suite.
echo "==[6/6] lenny-test --tier e2e_cloud==" >&2
ARTIFACT_BUCKET="$("${TF}" -chdir="${TF_DIR}" output -raw artifact_bucket)"
KMS_KEY_ARN="$("${TF}" -chdir="${TF_DIR}" output -raw kms_key_arn)"
export LENNY_CLOUD_PROVIDER=eks
export LENNY_AWS_ARTIFACT_BUCKET="${ARTIFACT_BUCKET}"
export LENNY_AWS_KMS_KEY_ARN="${KMS_KEY_ARN}"

# Install lenny-test if missing.
if ! command -v lenny-test >/dev/null 2>&1; then
  go install "${REPO_ROOT}/cmd/lenny-test"
fi
lenny-test --tier e2e_cloud --output human

echo "run-e2e.sh: tier-6 suite completed" >&2
