#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/run-load.sh — tier-12 cloud-load trigger.
#
# Pre-flight checks then triggers a run through the lenny-loadctl
# API. No terraform calls. No helm calls. No chart edits. No
# port-forwards.
#
# The provisioning pipeline is:
#
#   1. scripts/cloud/aws/up.sh            — EKS cluster + managed datastores
#   2. scripts/cloud/aws/install-lenny.sh — Helm install
#   3. scripts/cloud/aws/up-loadgen.sh    — runner pool (ASG + SQS + IAM)
#   4. scripts/cloud/aws/up-loadctl.sh    — control plane (ECS + RDS + ALB)
#   5. scripts/cloud/aws/run-load.sh      — THIS script: triggers a run
#
# Tear-down is the inverse order: down-loadctl.sh, down-loadgen.sh,
# then down.sh. Each `up-*` and `down-*` is idempotent and operator-
# invoked. run-load.sh never destroys infrastructure.
#
# Required environment:
#
#   AWS_PROFILE        — SSO profile.
#   AWS_REGION         — target region.
#   LENNY_RELEASE      — release name (must match up.sh release).
#
# Optional environment:
#
#   LENNY_LOAD_SCALE     — small|medium|production (default small)
#   LENNY_SCENARIOS      — comma-separated scenario list (default: tier-12 default set)
#   LENNY_LOADCTL_URL    — override the loadctl URL discovery
#   LENNY_LOADCTL_OPERATOR_TOKEN     — bearer token for the loadctl API
#
# Exit codes:
#   0   — run completed PASS
#   1   — run completed FAIL or ABORTED
#   2   — invalid flags/env
#   3   — pre-flight failure (cluster down, chart missing, runners
#         unhealthy, loadctl unreachable)
#
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${AWS_REGION:-us-west-2}"
SCALE="${LENNY_LOAD_SCALE:-small}"
SCENARIOS="${LENNY_SCENARIOS:-default}"

for cli in aws curl jq; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "run-load.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done

if ! aws sts get-caller-identity >/dev/null 2>&1; then
  echo "run-load.sh: aws sts get-caller-identity failed" >&2
  exit 3
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# Pre-flight 1: EKS cluster up.
echo "==[1/6] verifying EKS cluster ${LENNY_RELEASE}-eks" >&2
if ! aws eks describe-cluster --region "${REGION}" --name "${LENNY_RELEASE}-eks" >/dev/null 2>&1; then
  echo "  FAIL: EKS cluster ${LENNY_RELEASE}-eks not found. Run up.sh first." >&2
  exit 3
fi
echo "  OK"

# Pre-flight 2: Helm release present.
echo "==[2/6] verifying Helm release ${LENNY_RELEASE}" >&2
EKS_CONTEXT_NAME="arn:aws:eks:${REGION}:$(aws sts get-caller-identity --query Account --output text):cluster/${LENNY_RELEASE}-eks"
aws eks update-kubeconfig --region "${REGION}" --name "${LENNY_RELEASE}-eks" >/dev/null 2>&1 || true
if ! helm --kube-context "${EKS_CONTEXT_NAME}" status "${LENNY_RELEASE}" -n lenny-system >/dev/null 2>&1; then
  echo "  FAIL: Helm release ${LENNY_RELEASE} not installed. Run install-lenny.sh first." >&2
  exit 3
fi
echo "  OK"

# Pre-flight 3: runner pool healthy.
echo "==[3/6] verifying loadgen pool" >&2
ASG_NAME="${LENNY_RELEASE}-loadgen-runner"
DESIRED=$(aws autoscaling describe-auto-scaling-groups --auto-scaling-group-names "${ASG_NAME}" --region "${REGION}" --query 'AutoScalingGroups[0].DesiredCapacity' --output text 2>/dev/null || echo 0)
if [[ "${DESIRED}" == "None" || "${DESIRED}" == "0" || -z "${DESIRED}" ]]; then
  echo "  FAIL: loadgen ASG ${ASG_NAME} not present or scaled to 0. Run up-loadgen.sh first." >&2
  exit 3
fi
echo "  OK (desired=${DESIRED})"

# Pre-flight 4: loadctl reachable.
echo "==[4/6] verifying loadctl reachability" >&2
LOADCTL_URL="${LENNY_LOADCTL_URL:-}"
if [[ -z "${LOADCTL_URL}" ]]; then
  # Resolve via ALB DNS name tagged with this release.
  LOADCTL_URL=$(aws elbv2 describe-load-balancers --region "${REGION}" --query "LoadBalancers[?contains(LoadBalancerName, '${LENNY_RELEASE}-loadctl')].DNSName | [0]" --output text 2>/dev/null || echo "")
fi
if [[ -z "${LOADCTL_URL}" || "${LOADCTL_URL}" == "None" ]]; then
  echo "  FAIL: lenny-loadctl URL not resolvable. Run up-loadctl.sh first or set LENNY_LOADCTL_URL." >&2
  exit 3
fi
LOADCTL_URL="https://${LOADCTL_URL}"
if ! curl -fsS --max-time 10 "${LOADCTL_URL}/api/v1/runners" >/dev/null 2>&1; then
  echo "  WARN: loadctl ${LOADCTL_URL}/api/v1/runners did not return 200. Wave 6 wires the real service." >&2
fi
echo "  loadctl: ${LOADCTL_URL}"

# Pre-flight 5: bearer token.
if [[ -z "${LENNY_LOADCTL_OPERATOR_TOKEN:-}" ]]; then
  echo "==[5/6] WARN: LENNY_LOADCTL_OPERATOR_TOKEN not set; tier-12 API calls will be anonymous (Wave 6 will require auth)" >&2
fi

# Trigger run.
echo "==[6/6] triggering run scale=${SCALE} scenarios=${SCENARIOS}" >&2
RUN_REQ=$(jq -n --arg scale "${SCALE}" --arg sc "${SCENARIOS}" --arg release "${LENNY_RELEASE}" \
  '{scale: $scale, scenarios: ($sc | split(",")), cluster_release: $release}')
RUN_ID=$(curl -fsS -X POST "${LOADCTL_URL}/api/v1/runs" \
  -H "Authorization: Bearer ${LENNY_LOADCTL_OPERATOR_TOKEN:-}" \
  -H "Content-Type: application/json" \
  -d "${RUN_REQ}" \
  | jq -r '.id' 2>/dev/null || echo "")
if [[ -z "${RUN_ID}" || "${RUN_ID}" == "null" ]]; then
  echo "  FAIL: POST /api/v1/runs did not return a run ID. Wave 6 wires the real endpoint." >&2
  exit 1
fi
echo "  run_id=${RUN_ID}"

# Poll until terminal.
while true; do
  STATUS=$(curl -fsS "${LOADCTL_URL}/api/v1/runs/${RUN_ID}" -H "Authorization: Bearer ${LENNY_LOADCTL_OPERATOR_TOKEN:-}" | jq -r '.status')
  case "${STATUS}" in
    PASS)
      REPORT_URL=$(curl -fsS "${LOADCTL_URL}/api/v1/runs/${RUN_ID}/report" -H "Authorization: Bearer ${LENNY_LOADCTL_OPERATOR_TOKEN:-}" -o /dev/null -w '%{redirect_url}')
      echo "run-load.sh: PASS report=${REPORT_URL}"
      exit 0
      ;;
    FAIL|ABORTED)
      echo "run-load.sh: ${STATUS}"
      exit 1
      ;;
    PENDING|RUNNING)
      sleep 15
      ;;
    *)
      echo "run-load.sh: unexpected status ${STATUS}" >&2
      exit 1
      ;;
  esac
done
