#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/azure/run-load.sh — tier-7 cloud-load driver for Azure.
#
# Mirrors scripts/cloud/aws/run-load.sh. Provisions Azure Database for
# PostgreSQL Flexible Server + Azure Cache for Redis alongside the
# operator-supplied AKS cluster, applies the load fixture, port-
# forwards the gateway, and runs `lenny-test --tier load_cloud`.
#
# The AKS cluster itself is operator-supplied — the Lenny Terraform
# module does not create AKS / VNet / node-pools. Set AKS_CLUSTER_NAME
# / AZURE_RESOURCE_GROUP / AZURE_SUBSCRIPTION_ID before invoking.
#
# Required environment:
#
#   AZURE_SUBSCRIPTION_ID
#   AZURE_RESOURCE_GROUP
#   AKS_CLUSTER_NAME
#   ACR_REGISTRY                            ACR registry host (e.g.
#                                           lennye2e.azurecr.io). Images
#                                           must already be pushed.
#
# Optional environment:
#
#   AZURE_LOCATION                          default eastus.
#   LENNY_LOAD_SCALE=small|medium|production
#                                           sizing profile (default small).
#                                           Currently affects only the per-
#                                           mode pool sizes in the load
#                                           fixture; Azure terraform always
#                                           provisions the same SKU.
#   LENNY_RELEASE                           Helm release prefix. Default
#                                           lenny-load-{scale}.
#   IMAGE_TAG                               Image tag. Default 0.1.0.
#   KEEP_CLUSTER=1                          default. Skip terraform
#                                           destroy on exit.
#   LENNY_SKIP_TIER6_GATE=1                 bypass the tier-6 PASS gate.

set -euo pipefail

LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE:-small}"
AZURE_LOCATION="${AZURE_LOCATION:-eastus}"
RELEASE="${LENNY_RELEASE:-lenny-load-${LENNY_LOAD_SCALE}}"
LENNY_NAMESPACE="${LENNY_NAMESPACE:-lenny-system}"
IMAGE_TAG="${IMAGE_TAG:-0.1.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"

AZURE_SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID:?AZURE_SUBSCRIPTION_ID must be set}"
AZURE_RESOURCE_GROUP="${AZURE_RESOURCE_GROUP:?AZURE_RESOURCE_GROUP must be set}"
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:?AKS_CLUSTER_NAME must be set}"
ACR_REGISTRY="${ACR_REGISTRY:?ACR_REGISTRY must be set; push images first}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="${REPO_ROOT}/scripts/cloud/azure"

# Map LENNY_LOAD_SCALE → per-mode pool sizing placeholders. Identical
# to scripts/cloud/aws/run-load.sh so the load fixture renders the
# same per-mode envelope across providers.
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

for cli in az kubectl helm terraform envsubst jq; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "run-load.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done
if ! az account show --output none 2>/dev/null; then
  echo "run-load.sh: az login required (az login)" >&2
  exit 3
fi

# Tier-6 gate. Identical contract to scripts/cloud/aws/run-load.sh.
if [[ "${LENNY_SKIP_TIER6_GATE:-0}" != "1" ]]; then
  tier6_pass="$(jq -r 'select(.tiers.e2e_cloud == "pass") | .run_id' \
    "${REPO_ROOT}/tests/results/history.jsonl" 2>/dev/null | tail -n1)"
  if [[ -z "${tier6_pass}" ]]; then
    echo "run-load.sh: tier-6 e2e_cloud has not passed on this branch (tests/results/history.jsonl); " >&2
    echo "  the cloud-load run gates on a recent tier-6 PASS to avoid wasted spend on a known-broken cluster shape." >&2
    echo "  Run scripts/cloud/azure/run-e2e.sh first, or set LENNY_SKIP_TIER6_GATE=1 to bypass." >&2
    exit 4
  fi
  echo "run-load.sh: tier-6 PASS gate cleared (most-recent passing run ${tier6_pass})" >&2
fi

# 1. terraform apply for the per-release Azure resources, with
#    managed Postgres + Redis enabled.
echo "==[1/5] terraform apply (release=${RELEASE}, with-managed-datastores)==" >&2
ENV_OUT=$(AZURE_SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID}" \
  AZURE_RESOURCE_GROUP="${AZURE_RESOURCE_GROUP}" \
  AZURE_LOCATION="${AZURE_LOCATION}" \
  AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME}" \
  LENNY_NAMESPACE="${LENNY_NAMESPACE}" \
  WITH_FLEXIBLE_POSTGRES=true \
  WITH_AZURE_REDIS=true \
  bash "${SCRIPT_DIR}/up.sh" "${RELEASE}")
eval "${ENV_OUT}"

# 1b. Drain any leftover load-* SandboxWarmPools from a prior run, so
#     the helm rollout has CPU headroom against the warm-pool
#     sandboxes. Same contract as scripts/cloud/aws/run-load.sh.
if kubectl get namespace lenny-agents >/dev/null 2>&1; then
  echo "==[1b/5] drain leftover load-* SandboxWarmPools==" >&2
  kubectl -n lenny-agents delete sandboxwarmpool \
    load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool \
    --ignore-not-found
  for pool in load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool; do
    kubectl -n lenny-agents delete sandbox -l "lenny.dev/pool=${pool}" \
      --ignore-not-found --grace-period=1 --wait=false
  done
fi

# 2. Compose the managed DSN + Redis URL from the Key Vault secrets
#    that managed-services.tf wrote at terraform apply.
echo "==[2/5] compose managed DSN + Redis URL from Key Vault==" >&2
if [[ -z "${LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN}" || -z "${LENNY_AZURE_FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME}" ]]; then
  echo "run-load.sh: Flexible Server outputs missing; terraform apply must have failed silently" >&2
  exit 5
fi
pg_secret="$(az keyvault secret show \
  --vault-name "${LENNY_AZURE_KEY_VAULT_NAME}" \
  --name "${LENNY_AZURE_FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME}" \
  --query value --output tsv)"
pg_user="$(echo "${pg_secret}" | jq -r .username)"
pg_pass="$(echo "${pg_secret}" | jq -r .password)"
pg_pass_enc="$(jq -rn --arg p "${pg_pass}" '$p|@uri')"
LENNY_POSTGRES_DSN="postgres://${pg_user}:${pg_pass_enc}@${LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN}:5432/${LENNY_AZURE_FLEXIBLE_POSTGRES_DATABASE_NAME}?sslmode=require"

redis_secret="$(az keyvault secret show \
  --vault-name "${LENNY_AZURE_KEY_VAULT_NAME}" \
  --name "${LENNY_AZURE_REDIS_AUTH_SECRET_NAME}" \
  --query value --output tsv)"
redis_token="$(echo "${redis_secret}" | jq -r .auth)"
redis_token_enc="$(jq -rn --arg p "${redis_token}" '$p|@uri')"
# rediss:// — Azure Redis Cache enforces TLS-only above non_ssl_port.
LENNY_REDIS_URL="rediss://:${redis_token_enc}@${LENNY_AZURE_REDIS_HOSTNAME}:${LENNY_AZURE_REDIS_SSL_PORT}"
export LENNY_POSTGRES_DSN LENNY_REDIS_URL

# 3. Render the chart values overlay with the managed endpoints.
echo "==[3/5] render Helm values overlay==" >&2
VALUES_OUT="${SCRIPT_DIR}/values-cloud-azure.${RELEASE}.yaml"
ACR_REGISTRY="${ACR_REGISTRY}" IMAGE_TAG="${IMAGE_TAG}" \
  LENNY_POSTGRES_DSN="${LENNY_POSTGRES_DSN}" LENNY_REDIS_URL="${LENNY_REDIS_URL}" \
  LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID="${LENNY_AZURE_WORKLOAD_IDENTITY_CLIENT_ID}" \
  bash "${SCRIPT_DIR}/render-values.sh" "${VALUES_OUT}"

# 4. Apply CRDs + run lenny-migrate Job + helm upgrade.
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
          image: ${ACR_REGISTRY}/lenny-migrate:${IMAGE_TAG}
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

# 5. Apply the load fixture and run the suite.
echo "==[5/5] apply load fixture + run lenny-test --tier load_cloud==" >&2
export LOAD_RUNTIME_IMAGE="${ACR_REGISTRY}/lenny-runtime-echo:${IMAGE_TAG}"
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
  LENNY_LOAD_CLOUD_PROVIDERS=azure \
  LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE}" \
  go test -tags load_cloud -count=1 -timeout 60m -v "${REPO_ROOT}/tests/tier7_load_cloud/..."
