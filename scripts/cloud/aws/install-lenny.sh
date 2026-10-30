#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/install-lenny.sh — Helm install/upgrade of Lenny
# against an EKS cluster provisioned by up.sh.
#
# Idempotent. Safe to re-run against an existing install — `helm
# upgrade --install` reconciles the chart to the active values.
#
# Required environment:
#
#   AWS_PROFILE        — SSO profile.
#   AWS_REGION         — target region (default us-west-2).
#   LENNY_RELEASE      — Helm release name. Default lenny-load-small.
#
# Optional environment:
#
#   WITH_RDS=1         — route the gateway through managed RDS.
#   WITH_ELASTICACHE=1 — route the gateway through managed ElastiCache.
#
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${AWS_REGION:-us-west-2}"
WITH_RDS="${WITH_RDS:-1}"
WITH_ELASTICACHE="${WITH_ELASTICACHE:-1}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

for cli in aws kubectl helm; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "install-lenny.sh: required CLI ${cli} not on PATH" >&2
    exit 3
  fi
done
if ! aws sts get-caller-identity >/dev/null 2>&1; then
  echo "install-lenny.sh: aws sts get-caller-identity failed; run 'aws sso login --profile ${AWS_PROFILE:-<profile>}'" >&2
  exit 3
fi

EKS_CONTEXT_NAME="arn:aws:eks:${REGION}:$(aws sts get-caller-identity --query Account --output text):cluster/${LENNY_RELEASE}-eks"

echo "==> ensuring kubeconfig context for ${LENNY_RELEASE}-eks" >&2
aws eks update-kubeconfig --region "${REGION}" --name "${LENNY_RELEASE}-eks" >/dev/null

echo "==> rendering values" >&2
VALUES_FILE="$(mktemp -t lenny-load-values.yaml.XXXXXX)"
bash "${REPO_ROOT}/scripts/cloud/aws/render-values.sh" > "${VALUES_FILE}"

echo "==> helm upgrade --install" >&2
helm --kube-context "${EKS_CONTEXT_NAME}" upgrade --install "${LENNY_RELEASE}" \
  "${REPO_ROOT}/charts/lenny" \
  --namespace lenny-system --create-namespace \
  --values "${VALUES_FILE}" \
  --wait --timeout 10m

echo "==> waiting for gateway readiness" >&2
kubectl --context "${EKS_CONTEXT_NAME}" -n lenny-system rollout status deploy/lenny-gateway --timeout=5m

echo "install-lenny.sh: ${LENNY_RELEASE} ready"
