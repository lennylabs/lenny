#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/eks/up.sh — provisions the tier-6 EKS cluster.
#
# Runs `terraform apply` against deploy/terraform/cloud/aws/ with
# var.create_cluster=true so the apply produces a working EKS
# cluster + the per-release S3 bucket, KMS KEK, and IRSA role the
# Lenny chart consumes. Writes the kubeconfig context so kubectl
# reaches the cluster on return.
#
# Required environment:
#
#   AWS_PROFILE or AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY
#                       — the credentials Terraform + the aws CLI use.
#   AWS_REGION          — the target region. Default us-east-1.
#   LENNY_RELEASE       — the Helm release name + Terraform name
#                         prefix. Default lenny-e2e.
#
# Usage:
#
#   scripts/cloud/eks/up.sh                  # default cloud-small shape
#   scripts/cloud/eks/up.sh cloud-medium     # bumps node count + size

set -euo pipefail

SHAPE="${1:-cloud-small}"
REGION="${AWS_REGION:-us-east-1}"
RELEASE="${LENNY_RELEASE:-lenny-e2e}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

# Shape -> (node_instance_type, node_desired_size, node_max_size).
case "${SHAPE}" in
  cloud-small)
    NODE_TYPE="t3.medium"
    DESIRED=2
    MAX=4
    ;;
  cloud-medium)
    NODE_TYPE="m5.large"
    DESIRED=3
    MAX=6
    ;;
  cloud-large)
    NODE_TYPE="m5.xlarge"
    DESIRED=5
    MAX=10
    ;;
  *)
    echo "scripts/cloud/eks/up.sh: unknown shape ${SHAPE}; supported: cloud-small, cloud-medium, cloud-large" >&2
    exit 2
    ;;
esac

echo "eks/up.sh: shape=${SHAPE} region=${REGION} release=${RELEASE} node=${NODE_TYPE} desired=${DESIRED} max=${MAX}" >&2

# Stage tfvars: create_cluster=true so the apply produces the EKS
# cluster alongside the per-release resources.
cat > "${TFVARS_FILE}" <<JSON
{
  "release":            "${RELEASE}",
  "region":             "${REGION}",
  "create_cluster":     true,
  "kubernetes_version": "1.31",
  "node_instance_type": "${NODE_TYPE}",
  "node_desired_size":  ${DESIRED},
  "node_max_size":      ${MAX}
}
JSON

cd "${TF_DIR}"
terraform init -input=false
terraform apply -input=false -auto-approve -var-file="${TFVARS_FILE}"

# Sync kubeconfig so kubectl reaches the cluster.
aws eks --region "${REGION}" update-kubeconfig --name "${RELEASE}-eks"

echo "eks/up.sh: cluster ${RELEASE}-eks ready in region ${REGION}" >&2
echo "  artifact_bucket=$(terraform output -raw artifact_bucket)" >&2
echo "  kms_key_arn=$(terraform output -raw kms_key_arn)" >&2
echo "  cluster_endpoint=$(terraform output -raw cluster_endpoint)" >&2
