#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/run-load.sh — tier-7 cloud-load driver for AWS.
#
# The script provisions (or reuses) an EKS cluster sized for the
# active LENNY_LOAD_SCALE, brings up managed RDS Postgres + ElastiCache
# Redis alongside it so the gateway routes through managed datastores
# (the in-cluster postgres / redis pods are skipped), drains any
# leftover load-* warm pools from a prior run, applies the per-mode
# runtime fixture (tests/testinfra/k8s/agent-workload-load.yaml.tmpl),
# port-forwards the gateway, and runs `lenny-test --tier load_cloud`.
#
# Required environment:
#
#   AWS_PROFILE                       — SSO profile (e.g. lenny-tier6).
#   AWS_REGION                        — target region (default us-west-2).
#
# Optional environment:
#
#   LENNY_LOAD_SCALE=small|medium|production
#                                     — sizing profile. small (default)
#                                       targets cloud-small, medium
#                                       targets cloud-medium, production
#                                       targets cloud-large.
#   LENNY_RELEASE                     — Helm release / cluster prefix.
#                                       Default lenny-load-{scale}.
#   KEEP_CLUSTER=1                    — default. Skip the terraform destroy
#                                       on exit so the cluster stays up.
#   LENNY_LOAD_CLOUD_PROVIDERS        — auto-set to "aws" by the script.
#   LENNY_DETACH=1                    — re-exec the script inside a
#                                       detached `screen` session so a
#                                       short-lived caller (CI step,
#                                       interactive harness) does not
#                                       kill the in-flight bring-up.
#                                       The parent exits immediately
#                                       with the log path + session
#                                       name. Requires `screen`.
#
# The script gates on the tier-6 verdict: it refuses to run unless
# the most recent tier-6 e2e_cloud run on this branch reported PASS.
# Override with LENNY_SKIP_TIER6_GATE=1 (the gate exists because a
# cloud-load run that turns up a regression the tier-6 cluster-shape
# assertions also fail on is wasted spend).

set -euo pipefail

LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE:-small}"
REGION="${AWS_REGION:-us-west-2}"
RELEASE="${LENNY_RELEASE:-lenny-load-${LENNY_LOAD_SCALE}}"
KEEP_CLUSTER="${KEEP_CLUSTER:-1}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="${REPO_ROOT}/scripts/cloud/aws"

# Map LENNY_LOAD_SCALE → Terraform shape + per-mode pool sizing
# placeholders that envsubst resolves into the cloud-load
# runtime-fixture manifest. The numbers are deliberately set to a
# realistic envelope, not the §12.7 spec maximums.
case "${LENNY_LOAD_SCALE}" in
  small)
    SHAPE="cloud-small"
    export LOAD_SESSION_MIN=10  LOAD_SESSION_MAX=30
    export LOAD_CWORKSPACE_MIN=2 LOAD_CWORKSPACE_MAX=4 LOAD_CWORKSPACE_SLOTS=4
    export LOAD_CSTATELESS_MIN=2 LOAD_CSTATELESS_MAX=4 LOAD_CSTATELESS_SLOTS=8
    export LOAD_TASK_MIN=4 LOAD_TASK_MAX=12 LOAD_TASK_MAX_PER_POD=10
    ;;
  medium)
    SHAPE="cloud-medium"
    export LOAD_SESSION_MIN=30  LOAD_SESSION_MAX=100
    export LOAD_CWORKSPACE_MIN=4 LOAD_CWORKSPACE_MAX=10 LOAD_CWORKSPACE_SLOTS=8
    export LOAD_CSTATELESS_MIN=4 LOAD_CSTATELESS_MAX=10 LOAD_CSTATELESS_SLOTS=16
    export LOAD_TASK_MIN=10 LOAD_TASK_MAX=30 LOAD_TASK_MAX_PER_POD=20
    ;;
  production)
    SHAPE="cloud-large"
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

# Sanity prerequisites.
for cli in aws kubectl helm docker go envsubst; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "run-load.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done
if ! aws sts get-caller-identity >/dev/null 2>&1; then
  echo "run-load.sh: aws sts get-caller-identity failed; run 'aws sso login --profile ${AWS_PROFILE:-<profile>}'" >&2
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

# Tier-6 gate. The cloud-load run shares all cluster-shape
# preconditions with tier-6; failing tier-6 first means cloud-load
# is wasted spend. LENNY_SKIP_TIER6_GATE=1 bypasses (e.g. when
# explicitly driving a regression).
if [[ "${LENNY_SKIP_TIER6_GATE:-0}" != "1" ]]; then
  tier6_pass="$(jq -r 'select(.tiers.e2e_cloud == "pass") | .run_id' \
    "${REPO_ROOT}/tests/results/history.jsonl" 2>/dev/null | tail -n1)"
  if [[ -z "${tier6_pass}" ]]; then
    echo "run-load.sh: tier-6 e2e_cloud has not passed on this branch (tests/results/history.jsonl); " >&2
    echo "  the cloud-load run gates on a recent tier-6 PASS to avoid wasted spend on a known-broken cluster shape." >&2
    echo "  Run scripts/cloud/aws/run-e2e.sh first, or set LENNY_SKIP_TIER6_GATE=1 to bypass." >&2
    exit 4
  fi
  echo "run-load.sh: tier-6 PASS gate cleared (most-recent passing run ${tier6_pass})" >&2
fi

# 1. Cluster bring-up. Reuses scripts/cloud/aws/up.sh with the
#    matching shape and the managed-datastore flags tier-7 needs.
#    WITH_RDS=1 + WITH_ELASTICACHE=1 must be passed here AND on the
#    step-2 run-e2e.sh delegation: every up.sh invocation rewrites
#    deploy/terraform/cloud/aws/<release>.tfvars.json from its current
#    env, so a step-1 invocation without the flags would set
#    create_rds=false and `terraform apply` would DESTROY the managed
#    services that the next step is about to provision (or reuse).
#    On a repeat run the existing cluster + RDS + ElastiCache are
#    detected and apply is a no-op.
echo "==[1/5] terraform apply (shape=${SHAPE}, release=${RELEASE}, rds=1, elasticache=1)==" >&2
AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
  WITH_RDS=1 WITH_ELASTICACHE=1 WITH_ELASTICACHE_CLUSTER=0 \
  bash "${SCRIPT_DIR}/up.sh" "${SHAPE}"

# Pin every kubectl call to the EKS context this script provisioned
# so a parallel `kind create cluster` does not switch the current
# context out from under us mid-run.
EKS_CONTEXT_NAME="arn:aws:eks:${REGION}:$(aws sts get-caller-identity --query Account --output text):cluster/${RELEASE}-eks"
export KUBECTL_CONTEXT="${EKS_CONTEXT_NAME}"
kubectl() {
  command kubectl --context "${KUBECTL_CONTEXT}" "$@"
}
export -f kubectl
helm() {
  command helm --kube-context "${KUBECTL_CONTEXT}" "$@"
}
export -f helm

# 1b. Drain any leftover load-* SandboxWarmPools from a prior run.
#     The previous run's pools (4 pools × 2 CPU per sandbox at small
#     scale) pin both nodes at 100% CPU, blocking the rolling deploy of
#     gateway/webhook replicas in run-e2e.sh — `kubectl wait` then times
#     out on the datastore Deployments and the suite never reaches the
#     test-execution step. Deleting the pools triggers WPC drain; the
#     freed CPU is what lets the helm rollout converge. Step 4 re-
#     applies the runtime fixture, recreating the pools fresh against
#     the upgraded chart. Skip on a fresh cluster where the namespace
#     does not yet exist.
# The subshell wrap on the if-condition works around a bash 3.2
# quirk: under `set -e`, a `command kubectl` (inside the kubectl()
# wrapper function above) returning non-zero inside an `if` condition
# terminates the script even though the if is supposed to swallow the
# failure. macOS still ships bash 3.2 and the cloud scripts run there.
if (kubectl get namespace lenny-agents >/dev/null 2>&1); then
  echo "==[1b/5] drain leftover load-* SandboxWarmPools==" >&2
  # The four pool names are deterministic across runs; deleting them
  # before re-applying the load fixture frees the CPU the prior run's
  # warm pods were holding. Force-drain the orphaned Sandboxes after
  # so CPU frees up immediately instead of waiting on the per-sandbox
  # §6.2 drain timeout. Both deletes are idempotent.
  kubectl -n lenny-agents delete sandboxwarmpool \
    load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool \
    --ignore-not-found
  for pool in load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool; do
    kubectl -n lenny-agents delete sandbox -l "lenny.dev/pool=${pool}" \
      --ignore-not-found --grace-period=1 --wait=false
  done
fi

# 2. Apply the Lenny chart (reuse the tier-6 run-e2e.sh's chart
#    install path so the gateway + admission webhooks come up the
#    same way). Tier-7 runs the gateway against managed RDS Postgres
#    + ElastiCache (non-cluster-mode) so the in-cluster postgres /
#    redis pods (~1.6 CPU) do not steal capacity from the warm-pool
#    sandboxes. The chart uses gateway-process-local Redis client
#    (pkg/redisconn), which does not support cluster-mode, so the
#    standard single-shard replication group is the right target;
#    WITH_ELASTICACHE_CLUSTER stays off here (tier-6's cluster-mode
#    tests own that path).
echo "==[2/5] reuse tier-6 install path (delegating to run-e2e.sh)==" >&2
AWS_REGION="${REGION}" LENNY_RELEASE="${RELEASE}" \
  WITH_RDS=1 WITH_ELASTICACHE=1 WITH_ELASTICACHE_CLUSTER=0 \
  KEEP_CLUSTER=1 \
  LENNY_CLOUD_PROVIDERS=aws \
  bash "${SCRIPT_DIR}/run-e2e.sh"

# 3. Resolve the runtime image the cloud-load fixture references.
#    The chart's reference-runtimes catalog is disabled on this
#    overlay (the load fixture owns the runtime registrations);
#    the load runtime image is the lenny-runtime-echo we pushed
#    alongside the chart images.
echo "==[3/5] resolve LOAD_RUNTIME_IMAGE (ECR-hosted lenny-runtime-echo)==" >&2
ECR_REGISTRY="$(aws sts get-caller-identity --query Account --output text).dkr.ecr.${REGION}.amazonaws.com"
LOAD_IMAGE_TAG="$("${SCRIPT_DIR}/source-hash.sh")"
export LOAD_RUNTIME_IMAGE="${ECR_REGISTRY}/lenny-runtime-echo:${LOAD_IMAGE_TAG}"
echo "  LOAD_RUNTIME_IMAGE=${LOAD_RUNTIME_IMAGE}" >&2

# 4. Render + apply the per-mode runtime fixture.
echo "==[4/5] apply tier-7 load runtime fixture==" >&2
envsubst < "${REPO_ROOT}/tests/testinfra/k8s/agent-workload-load.yaml.tmpl" \
  | kubectl apply -f -
# Wait for each pool to reach minWarm before driving load against it.
for pool in load-session-pool load-cworkspace-pool load-cstateless-pool load-task-pool; do
  echo "  waiting for ${pool} to scale to minWarm..." >&2
  until [[ "$(kubectl -n lenny-agents get sandboxwarmpool "${pool}" -o jsonpath='{.status.readyCount}' 2>/dev/null)" -ge 1 ]]; do
    sleep 5
  done
  ready=$(kubectl -n lenny-agents get sandboxwarmpool "${pool}" -o jsonpath='{.status.readyCount}')
  echo "  ${pool} ready=${ready}" >&2
done

# 5. Port-forward the gateway and run the cloud-load suite. The
#    suite reads LENNY_GATEWAY_BASE_URL and LENNY_LOAD_CLOUD_PROVIDERS
#    through the harness; we set both here.
echo "==[5/5] lenny-test --tier load_cloud==" >&2
kubectl -n lenny-system port-forward svc/lenny-gateway 28080:8080 >/tmp/lenny-load-pf.log 2>&1 &
pf_pid=$!
trap 'kill ${pf_pid} 2>/dev/null || true' EXIT
sleep 3

LENNY_GATEWAY_BASE_URL="http://127.0.0.1:28080" \
  LENNY_LOAD_CLOUD_PROVIDERS=aws \
  LENNY_LOAD_SCALE="${LENNY_LOAD_SCALE}" \
  go test -tags load_cloud -count=1 -timeout 60m -v "${REPO_ROOT}/tests/tier7_load_cloud/..."
