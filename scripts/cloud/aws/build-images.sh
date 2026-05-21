#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/build-images.sh — build + push the Lenny platform
# images to ECR for a tier-6 EKS run.
#
# For each image listed in IMAGES below, the script:
#
#   1. Ensures the ECR repository exists (creating it if absent).
#   2. Logs the local Docker into ECR using the operator's AWS
#      session.
#   3. Builds the image from the root Dockerfile with BINARY=<name>.
#   4. Pushes the image with the configured TAG (the chart's
#      appVersion by default).
#
# Required environment:
#
#   AWS_PROFILE or static AWS creds + AWS_REGION (default us-west-2)
#
# Optional overrides:
#
#   IMAGE_TAG   Tag to apply (default "0.1.0", matching Chart.yaml).
#   PLATFORMS   docker buildx --platform list (default linux/amd64).
#
# On success the script prints `ECR_REGISTRY=<account>.dkr.ecr.<region>.amazonaws.com`
# so the caller can capture it for the values overlay.

set -euo pipefail

REGION="${AWS_REGION:-us-west-2}"
TAG="${IMAGE_TAG:-0.1.0}"
PLATFORMS="${PLATFORMS:-linux/amd64}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# IMAGES is the list of (image name, BINARY build-arg) pairs the chart
# consumes. Order matters only for log readability.
IMAGES=(
  "lenny-controller:lenny-controller"
  "lenny-webhook:lenny-webhook"
  "lenny-token-service:lenny-token-service"
  "lenny-gateway:lenny-gateway"
  "lenny-ops:lenny-ops"
  "lenny-preflight:lenny-preflight"
  "lenny-backup:lenny-backup"
  "lenny-ctl:lenny-ctl"
  "lenny-adapter:lenny-adapter"
  "lenny-migrate:lenny-migrate"
)

ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
ECR_REGISTRY="${ACCOUNT}.dkr.ecr.${REGION}.amazonaws.com"

echo "build-images.sh: account=${ACCOUNT} region=${REGION} registry=${ECR_REGISTRY} tag=${TAG}" >&2

# One-time ECR login.
aws ecr get-login-password --region "${REGION}" \
  | docker login --username AWS --password-stdin "${ECR_REGISTRY}" >&2

cd "${REPO_ROOT}"
built=0
skipped=0
for entry in "${IMAGES[@]}"; do
  name="${entry%:*}"
  binary="${entry#*:}"
  ref="${ECR_REGISTRY}/${name}:${TAG}"

  # Ensure the ECR repo exists. DescribeRepositories surfaces a
  # RepositoryNotFoundException when the repo is absent; the create
  # is the recovery.
  if ! aws ecr describe-repositories --region "${REGION}" \
      --repository-names "${name}" >/dev/null 2>&1; then
    echo "build-images.sh: creating ECR repo ${name}" >&2
    aws ecr create-repository --region "${REGION}" \
      --repository-name "${name}" \
      --image-scanning-configuration scanOnPush=true >/dev/null
  fi

  # Skip build + push when the tag already exists in ECR. The caller
  # uses a content-addressable tag (source-hash.sh) so an existing
  # tag means the source has not changed since the last build.
  if aws ecr describe-images --region "${REGION}" \
      --repository-name "${name}" \
      --image-ids "imageTag=${TAG}" >/dev/null 2>&1; then
    echo "build-images.sh: ${name}:${TAG} already in ECR; skipping" >&2
    skipped=$((skipped + 1))
    continue
  fi

  echo "build-images.sh: building ${name} (BINARY=${binary})" >&2
  # Build with buildx so cross-arch works the same way the release
  # workflow does. For a single platform a local docker build would
  # work too; buildx makes the script forward-compatible with
  # multi-arch.
  docker buildx build \
    --platform "${PLATFORMS}" \
    --build-arg "BINARY=${binary}" \
    --tag "${ref}" \
    --push \
    --file "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"
  built=$((built + 1))
done

echo "build-images.sh: built ${built}, skipped ${skipped} (tag ${TAG})" >&2
echo "ECR_REGISTRY=${ECR_REGISTRY}"
