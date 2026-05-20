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

# 4. In-cluster datastores: Postgres + Redis + MinIO fixtures. The
#    same manifest the Kind e2e overlay uses works inside EKS too —
#    the storage tier is emptyDir which is correct for an ephemeral
#    test cluster.
echo "==[4/6] apply in-cluster datastores==" >&2
kubectl create namespace lenny-system --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n lenny-system -f "${REPO_ROOT}/tests/testinfra/kind/datastores.yaml"
kubectl wait -n lenny-system --for=condition=available --timeout=300s \
  deployment/lenny-postgres deployment/lenny-redis deployment/lenny-minio

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
