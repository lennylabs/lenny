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
# cluster alongside the per-release resources. The optional
# create_rds + create_elasticache flags add managed-service
# provisioning when the caller sets WITH_RDS=1 / WITH_ELASTICACHE=1
# in the environment.
#
# TEST_CIDR (single CIDR for the caller's home/office IP) admits
# external Postgres + Redis connections so the tier-6 managed-
# service tests can run from outside the VPC. Defaults to the
# caller's current public IP fetched from checkip.amazonaws.com.
WITH_RDS="${WITH_RDS:-0}"
WITH_ELASTICACHE="${WITH_ELASTICACHE:-0}"
RDS_MULTI_AZ="${RDS_MULTI_AZ:-0}"
ELASTICACHE_REPLICAS="${ELASTICACHE_REPLICAS:-0}"
ELASTICACHE_SHARDS="${ELASTICACHE_SHARDS:-1}"
TEST_CIDR="${TEST_CIDR:-$(curl -s https://checkip.amazonaws.com 2>/dev/null | tr -d '[:space:]')/32}"
if [[ "${TEST_CIDR}" == "/32" ]]; then
  TEST_CIDR=""
fi
cidr_array="[]"
if [[ -n "${TEST_CIDR}" ]]; then
  cidr_array="[\"${TEST_CIDR}\"]"
fi
cat > "${TFVARS_FILE}" <<JSON
{
  "release":            "${RELEASE}",
  "region":             "${REGION}",
  "create_cluster":     true,
  "kubernetes_version": "1.31",
  "node_instance_type": "${NODE_TYPE}",
  "node_desired_size":  ${DESIRED},
  "node_max_size":      ${MAX},
  "create_rds":         $([[ "${WITH_RDS}" == "1" ]] && echo true || echo false),
  "rds_multi_az":       $([[ "${RDS_MULTI_AZ}" == "1" ]] && echo true || echo false),
  "create_elasticache": $([[ "${WITH_ELASTICACHE}" == "1" ]] && echo true || echo false),
  "elasticache_num_node_groups":         ${ELASTICACHE_SHARDS},
  "elasticache_replicas_per_node_group": ${ELASTICACHE_REPLICAS},
  "managed_datastores_test_cidrs":       ${cidr_array}
}
JSON

# Pick terraform if installed; otherwise fall back to opentofu's
# `tofu` binary (drop-in replacement for terraform).
if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "eks/up.sh: neither terraform nor tofu on PATH; install one of:" >&2
  echo "  brew install hashicorp/tap/terraform" >&2
  echo "  brew install opentofu" >&2
  exit 3
fi

cd "${TF_DIR}"
${TF} init -input=false
${TF} apply -input=false -auto-approve -var-file="${TFVARS_FILE}"

# Sync kubeconfig so kubectl reaches the cluster.
aws eks --region "${REGION}" update-kubeconfig --name "${RELEASE}-eks"

echo "eks/up.sh: cluster ${RELEASE}-eks ready in region ${REGION}" >&2
echo "  artifact_bucket=$(${TF} output -raw artifact_bucket)" >&2
echo "  kms_key_arn=$(${TF} output -raw kms_key_arn)" >&2
echo "  cluster_endpoint=$(${TF} output -raw cluster_endpoint)" >&2
