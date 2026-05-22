#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gcp/install-lenny.sh — Helm install/upgrade of Lenny
# against a GKE cluster provisioned by up.sh. Idempotent.
# TESTING.md §12.12 / Wave 5.

set -euo pipefail

LENNY_RELEASE="${LENNY_RELEASE:-lenny-load-small}"
REGION="${GCP_REGION:-us-central1}"
PROJECT_ID="${GCP_PROJECT:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

if [[ -z "${PROJECT_ID}" ]]; then
  echo "install-lenny.sh: GCP_PROJECT is required" >&2; exit 2
fi
for cli in gcloud kubectl helm; do
  command -v "${cli}" >/dev/null 2>&1 || { echo "install-lenny.sh: ${cli} not on PATH" >&2; exit 3; }
done

gcloud container clusters get-credentials "${LENNY_RELEASE}-gke" --region "${REGION}" --project "${PROJECT_ID}" >/dev/null

VALUES_FILE="$(mktemp -t lenny-load-values.yaml.XXXXXX)"
bash "${REPO_ROOT}/scripts/cloud/gcp/render-values.sh" > "${VALUES_FILE}"

helm upgrade --install "${LENNY_RELEASE}" "${REPO_ROOT}/charts/lenny" \
  --namespace lenny-system --create-namespace \
  --values "${VALUES_FILE}" \
  --wait --timeout 10m

kubectl -n lenny-system rollout status deploy/lenny-gateway --timeout=5m
echo "install-lenny.sh: ${LENNY_RELEASE} ready"
