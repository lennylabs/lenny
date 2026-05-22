#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/run-load.sh — tier-7 cloud-load driver for GCP.
#
# Mirrors scripts/cloud/aws/run-load.sh. Provisions Cloud SQL for
# PostgreSQL + Memorystore for Redis alongside the operator-supplied
# GKE cluster, applies the load fixture, port-forwards the gateway,
# and runs `lenny-test --tier load_cloud`.
#
# The GKE cluster + VPC + node pools are operator-supplied. Both
# managed services attach to the cluster's VPC via Private Service
# Access (the operator must have configured the VPC's PSA allocation
# at cluster creation time, outside this module).
#
# Required environment:
#
#   GCP_PROJECT
#   GCP_REGION                              default us-central1.
#   GKE_CLUSTER_NAME
#   GKE_CLUSTER_LOCATION
#   AR_REGISTRY                             Artifact Registry repo path
#                                           (e.g. us-central1-docker.pkg.dev/acme/lenny).
#                                           Images must already be pushed.
#   MANAGED_DATASTORES_NETWORK              Self-link of the VPC the
#                                           GKE cluster uses, for the
#                                           Cloud SQL / Memorystore PSA
#                                           attachment.
#
# Optional environment:
#
#   LENNY_LOAD_SCALE=small|medium|production    sizing profile.
#   LENNY_RELEASE                               default lenny-load-{scale}.
#   IMAGE_TAG                                   default 0.1.0.
#   KEEP_CLUSTER=1                              default. Skip terraform destroy.
#   LENNY_SKIP_TIER6_GATE=1                     bypass tier-6 PASS gate.
#   LENNY_DETACH=1                              re-exec the script
#                                               inside a detached
#                                               `screen` session so a
#                                               short-lived caller (CI
#                                               step, interactive
#                                               harness) does not kill
#                                               the in-flight bring-up.
#                                               The parent exits
#                                               immediately with the
#                                               log path + session name.
#                                               Requires `screen`.

set -euo pipefail

LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE:-small}"
GCP_REGION="${GCP_REGION:-us-central1}"
RELEASE="${LENNY_RELEASE:-lenny-load-${LENNY_LOAD_SCALE}}"
LENNY_NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"
IMAGE_TAG="${IMAGE_TAG:-0.1.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"

GCP_PROJECT="${GCP_PROJECT:?GCP_PROJECT must be set}"
GKE_CLUSTER_NAME="${GKE_CLUSTER_NAME:?GKE_CLUSTER_NAME must be set}"
GKE_CLUSTER_LOCATION="${GKE_CLUSTER_LOCATION:?GKE_CLUSTER_LOCATION must be set}"
AR_REGISTRY="${AR_REGISTRY:?AR_REGISTRY must be set; push images first}"
MANAGED_DATASTORES_NETWORK="${MANAGED_DATASTORES_NETWORK:?MANAGED_DATASTORES_NETWORK must be set (VPC self-link the GKE cluster uses)}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="${REPO_ROOT}/scripts/cloud/gcp"

case "${LENNY_LOAD_SCALE}" in
  small)
    export LOAD_SESSION_MIN=10 LOAD_SESSION_MAX=30
    export LOAD_CWORKSPACE_MIN=2 LOAD_CWORKSPACE_MAX=4 LOAD_CWORKSPACE_SLOTS=4
    export LOAD_CSTATELESS_MIN=2 LOAD_CSTATELESS_MAX=4 LOAD_CSTATELESS_SLOTS=8
    export LOAD_TASK_MIN=4 LOAD_TASK_MAX=12 LOAD_TASK_MAX_PER_POD=10
    ;;
  medium)
    export LOAD_SESSION_MIN=30 LOAD_SESSION_MAX=100
    export LOAD_CWORKSPACE_MIN=4 LOAD_CWORKSPACE_MAX=10 LOAD_CWORKSPACE_SLOTS=8
    export LOAD_CSTATELESS_MIN=4 LOAD_CSTATELESS_MAX=10 LOAD_CSTATELESS_SLOTS=16
    export LOAD_TASK_MIN=10 LOAD_TASK_MAX=30 LOAD_TASK_MAX_PER_POD=20
    ;;
  production)
    export LOAD_SESSION_MIN=100 LOAD_SESSION_MAX=500
    export LOAD_CWORKSPACE_MIN=10 LOAD_CWORKSPACE_MAX=30 LOAD_CWORKSPACE_SLOTS=16
    export LOAD_CSTATELESS_MIN=10 LOAD_CSTATELESS_MAX=30 LOAD_CSTATELESS_SLOTS=32
    export LOAD_TASK_MIN=30 LOAD_TASK_MAX=100 LOAD_TASK_MAX_PER_POD=50
    ;;
  *)
    echo "run-load.sh: unknown LENNY_LOAD_SCALE=${LENNY_LOAD_SCALE}; supported: small, medium, production" >&2
    exit 2
    ;;
esac

for cli in gcloud kubectl helm terraform envsubst jq; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "run-load.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done
if ! gcloud auth print-identity-token --quiet >/dev/null 2>&1; then
  echo "run-load.sh: gcloud auth login required (gcloud auth application-default login)" >&2
  exit 3
fi

# LENNY_DETACH=1 re-execs the script inside a detached `screen`
# session so a parent process with a short lifetime (a CI step that
# polls for completion via the log, an editor terminal, an
# interactive harness with a process-tree timeout) does not kill the
# in-flight cluster bring-up. The detached child re-runs the script
# with LENNY_DETACH=0 so it executes inline. The session name is
# scoped by release + scale so two concurrent scales do not collide.
if [[ "${LENNY_DETACH:-0}" == "1" ]]; then
  if ! command -v screen >/dev/null 2>&1; then
    echo "run-load.sh: LENNY_DETACH=1 requires 'screen' on PATH (brew install screen / apt install screen)" >&2
    exit 3
  fi
  SCREEN_NAME="lenny-load-${RELEASE}-${LENNY_LOAD_SCALE}"
  LOG_FILE="/tmp/${SCREEN_NAME}.log"
  if screen -ls "${SCREEN_NAME}" 2>/dev/null | grep -q "[0-9]\.${SCREEN_NAME}"; then
    echo "run-load.sh: detached session ${SCREEN_NAME} is already running" >&2
    echo "  tail:   tail -F ${LOG_FILE}" >&2
    echo "  attach: screen -r ${SCREEN_NAME}" >&2
    echo "  cancel: screen -S ${SCREEN_NAME} -X quit" >&2
    exit 1
  fi
  : > "${LOG_FILE}"
  screen -dmS "${SCREEN_NAME}" bash -c "LENNY_DETACH=0 bash '${SCRIPT_DIR}/run-load.sh' >> '${LOG_FILE}' 2>&1; echo \"run-load.sh: exit=\$?\" >> '${LOG_FILE}'"
  echo "run-load.sh: detached as screen session ${SCREEN_NAME}" >&2
  echo "  log:    ${LOG_FILE}" >&2
  echo "  tail:   tail -F ${LOG_FILE}" >&2
  echo "  attach: screen -r ${SCREEN_NAME}" >&2
  echo "  cancel: screen -S ${SCREEN_NAME} -X quit" >&2
  exit 0
fi

# Tier-6 gate.
if [[ "${LENNY_SKIP_TIER6_GATE:-0}" != "1" ]]; then
  tier6_pass="$(jq -r 'select(.tiers.e2e_cloud == "pass") | .run_id' \
    "${REPO_ROOT}/tests/results/history.jsonl" 2>/dev/null | tail -n1)"
  if [[ -z "${tier6_pass}" ]]; then
    echo "run-load.sh: tier-6 e2e_cloud has not passed on this branch (tests/results/history.jsonl); " >&2
    echo "  the cloud-load run gates on a recent tier-6 PASS to avoid wasted spend on a known-broken cluster shape." >&2
    echo "  Run scripts/cloud/gcp/run-e2e.sh first, or set LENNY_SKIP_TIER6_GATE=1 to bypass." >&2
    exit 4
  fi
  echo "run-load.sh: tier-6 PASS gate cleared (most-recent passing run ${tier6_pass})" >&2
fi

echo "==[1/5] terraform apply (release=${RELEASE}, with-managed-datastores)==" >&2
ENV_OUT=$(GCP_PROJECT="${GCP_PROJECT}" \
  GCP_REGION="${GCP_REGION}" \
  GKE_CLUSTER_NAME="${GKE_CLUSTER_NAME}" \
  GKE_CLUSTER_LOCATION="${GKE_CLUSTER_LOCATION}" \
  LENNY_NAMESPACE="${LENNY_NAMESPACE}" \
  WITH_CLOUD_SQL=true \
  WITH_MEMORYSTORE=true \
  MANAGED_DATASTORES_NETWORK="${MANAGED_DATASTORES_NETWORK}" \
  bash "${SCRIPT_DIR}/up.sh" "${RELEASE}")
eval "${ENV_OUT}"

# Pin every kubectl + helm call to the GKE context up.sh just merged
# so a parallel `kind create cluster` does not switch
# ~/.kube/config's current context out from under us mid-run. The
# context name gcloud container clusters get-credentials writes is
# gke_<project>_<location>_<cluster>.
export KUBECTL_CONTEXT="gke_${GCP_PROJECT}_${GKE_CLUSTER_LOCATION}_${GKE_CLUSTER_NAME}"
kubectl() {
  command kubectl --context "${KUBECTL_CONTEXT}" "$@"
}
export -f kubectl
helm() {
  command helm --kube-context "${KUBECTL_CONTEXT}" "$@"
}
export -f helm

# Subshell wrap works around a bash 3.2 quirk; see the AWS sibling.
if (kubectl get namespace lenny-agents >/dev/null 2>&1); then
  echo "==[1b/5] drain leftover load-* SandboxWarmPools==" >&2
  kubectl -n lenny-agents delete sandboxwarmpool \
    load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool \
    --ignore-not-found
  for pool in load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool; do
    kubectl -n lenny-agents delete sandbox -l "lenny.dev/pool=${pool}" \
      --ignore-not-found --grace-period=1 --wait=false
  done
fi

echo "==[2/5] compose managed DSN + Redis URL from Secret Manager==" >&2
if [[ -z "${LENNY_GCP_CLOUD_SQL_ADMIN_SECRET_NAME}" ]]; then
  echo "run-load.sh: Cloud SQL outputs missing; terraform apply must have failed silently" >&2
  exit 5
fi
pg_secret="$(gcloud secrets versions access latest \
  --project "${GCP_PROJECT}" \
  --secret "${LENNY_GCP_CLOUD_SQL_ADMIN_SECRET_NAME}")"
pg_user="$(echo "${pg_secret}" | jq -r .username)"
pg_pass="$(echo "${pg_secret}" | jq -r .password)"
pg_host="$(echo "${pg_secret}" | jq -r .host)"
pg_pass_enc="$(jq -rn --arg p "${pg_pass}" '$p|@uri')"
LENNY_POSTGRES_DSN="postgres://${pg_user}:${pg_pass_enc}@${pg_host}:5432/${LENNY_GCP_CLOUD_SQL_DATABASE_NAME}?sslmode=require"

redis_secret="$(gcloud secrets versions access latest \
  --project "${GCP_PROJECT}" \
  --secret "${LENNY_GCP_MEMORYSTORE_AUTH_SECRET_NAME}")"
redis_token="$(echo "${redis_secret}" | jq -r .auth)"
redis_token_enc="$(jq -rn --arg p "${redis_token}" '$p|@uri')"
# rediss:// — Memorystore enforces TLS when transit_encryption_mode is SERVER_AUTHENTICATION.
LENNY_REDIS_URL="rediss://:${redis_token_enc}@${LENNY_GCP_MEMORYSTORE_HOST}:${LENNY_GCP_MEMORYSTORE_PORT}"
export LENNY_POSTGRES_DSN LENNY_REDIS_URL

echo "==[3/5] render Helm values overlay==" >&2
VALUES_OUT="${SCRIPT_DIR}/values-cloud-gcp.${RELEASE}.yaml"
# Cloud SQL + Memorystore attach to the GKE VPC via Private Service
# Access. The chart's gateway / token-service NetworkPolicy must
# admit egress to the PSA range; the per-VPC PSA allocation prefix
# is the cluster's `services_ipv4_cidr_block`. The operator may set
# MANAGED_DATASTORES_EGRESS_CIDR to a tighter range; the default
# 0.0.0.0/0 is bounded by per-port (5432 / 6379).
AR_REGISTRY="${AR_REGISTRY}" IMAGE_TAG="${IMAGE_TAG}" \
  LENNY_POSTGRES_DSN="${LENNY_POSTGRES_DSN}" LENNY_REDIS_URL="${LENNY_REDIS_URL}" \
  LENNY_GCP_SERVICE_ACCOUNT_EMAIL="${LENNY_GCP_SERVICE_ACCOUNT_EMAIL}" \
  POSTGRES_GATEWAY_EGRESS_CIDR="${MANAGED_DATASTORES_EGRESS_CIDR:-0.0.0.0/0}" \
  REDIS_GATEWAY_EGRESS_CIDR="${MANAGED_DATASTORES_EGRESS_CIDR:-0.0.0.0/0}" \
  REDIS_GATEWAY_EGRESS_PORT="${LENNY_GCP_MEMORYSTORE_PORT}" \
  bash "${SCRIPT_DIR}/render-values.sh" "${VALUES_OUT}"

echo "==[4/5] CRDs + migrate + helm upgrade ${RELEASE}==" >&2
kubectl apply -f "${REPO_ROOT}/charts/lenny/crds/"
kubectl create namespace "${LENNY_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "${LENNY_NAMESPACE}" delete job lenny-load-migrate --ignore-not-found --wait=true >/dev/null
cat <<MIGRATE | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: lenny-load-migrate
  namespace: ${LENNY_NAMESPACE}
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
          image: ${AR_REGISTRY}/lenny-migrate:${IMAGE_TAG}
          imagePullPolicy: IfNotPresent
          args: ["up"]
          env:
            - name: LENNY_POSTGRES_DSN
              value: "${LENNY_POSTGRES_DSN}"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
MIGRATE
kubectl -n "${LENNY_NAMESPACE}" wait --for=condition=complete job/lenny-load-migrate --timeout=600s

helm upgrade --install "${RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace "${LENNY_NAMESPACE}" --create-namespace \
  --values "${VALUES_OUT}" \
  --wait --timeout 10m

echo "==[5/5] apply load fixture + run lenny-test --tier load_cloud==" >&2
export LOAD_RUNTIME_IMAGE="${AR_REGISTRY}/lenny-runtime-echo:${IMAGE_TAG}"
envsubst < "${REPO_ROOT}/tests/testinfra/k8s/agent-workload-load.yaml.tmpl" \
  | kubectl apply -f -
for pool in load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool; do
  echo "  waiting for ${pool} to scale to minWarm..." >&2
  until [[ "$(kubectl -n lenny-agents get sandboxwarmpool "${pool}" -o jsonpath='{.status.readyCount}' 2>/dev/null)" -ge 1 ]]; do
    sleep 5
  done
  echo "  ${pool} ready" >&2
done

kubectl -n "${LENNY_NAMESPACE}" port-forward svc/lenny-gateway 28080:8080 >/tmp/lenny-load-pf.log 2>&1 &
pf_pid=$!
trap 'kill ${pf_pid} 2>/dev/null || true' EXIT
sleep 3

LENNY_GATEWAY_BASE_URL="http://127.0.0.1:28080" \
  LENNY_LOAD_CLOUD_PROVIDERS=gcp \
  LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE}" \
  go test -tags load_cloud -count=1 -timeout 60m -v "${REPO_ROOT}/tests/tier7_load_cloud/..."
