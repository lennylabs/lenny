#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/aws/up.sh — provisions the tier-6 EKS cluster.
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
#   scripts/cloud/aws/up.sh                  # default cloud-small shape
#   scripts/cloud/aws/up.sh cloud-medium     # bumps node count + size

set -euo pipefail

SHAPE="${1:-cloud-small}"
REGION="${AWS_REGION:-us-east-1}"
RELEASE="${LENNY_RELEASE:-lenny-e2e}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TF_DIR="${REPO_ROOT}/deploy/terraform/cloud/aws"
TFVARS_FILE="${TF_DIR}/${RELEASE}.tfvars.json"

# Shape -> (node_instance_type, node_desired_size, node_max_size).
# The cloud-* shapes are sized for the tier-6 conformance suite (no
# load fixture, no warm-pool churn). The load-* shapes are sized for
# tier-7 cloud-load: each scale's warm pools alone add ~25 / ~55 /
# ~175 vCPU of pod requests on top of the chart workloads, so the
# tier-6 shapes are dramatically undersized for tier-7. NODE_DESIRED,
# NODE_MAX, and NODE_INSTANCE_TYPE env vars override the shape
# defaults so a caller (run-load.sh, an ad-hoc operator) can tune
# without picking a new shape; the override is also how
# run-e2e.sh's chart-rollout-capacity heuristic bumps node count
# when managed services are on.
case "${SHAPE}" in
  cloud-small)
    NODE_TYPE="${NODE_INSTANCE_TYPE:-t3.medium}"
    DESIRED="${NODE_DESIRED:-2}"
    MAX="${NODE_MAX:-4}"
    ;;
  cloud-medium)
    NODE_TYPE="${NODE_INSTANCE_TYPE:-m5.large}"
    DESIRED="${NODE_DESIRED:-3}"
    MAX="${NODE_MAX:-6}"
    ;;
  cloud-large)
    NODE_TYPE="${NODE_INSTANCE_TYPE:-m5.xlarge}"
    DESIRED="${NODE_DESIRED:-5}"
    MAX="${NODE_MAX:-10}"
    ;;
  load-small)
    # ~25 vCPU pod requests: 6 × m5.xlarge (4 vCPU each) = 24 vCPU at
    # max scale; desired starts at 3 (12 vCPU) and the EKS managed
    # nodegroup picks up the rest when warm pools fill.
    NODE_TYPE="${NODE_INSTANCE_TYPE:-m5.xlarge}"
    DESIRED="${NODE_DESIRED:-3}"
    MAX="${NODE_MAX:-6}"
    ;;
  load-medium)
    # ~55 vCPU pod requests: 8 × m5.2xlarge (8 vCPU each) = 64 vCPU at max.
    NODE_TYPE="${NODE_INSTANCE_TYPE:-m5.2xlarge}"
    DESIRED="${NODE_DESIRED:-4}"
    MAX="${NODE_MAX:-8}"
    ;;
  load-production)
    # ~175 vCPU pod requests: 16 × m5.4xlarge (16 vCPU each) = 256 vCPU at max.
    NODE_TYPE="${NODE_INSTANCE_TYPE:-m5.4xlarge}"
    DESIRED="${NODE_DESIRED:-8}"
    MAX="${NODE_MAX:-16}"
    ;;
  *)
    echo "scripts/cloud/aws/up.sh: unknown shape ${SHAPE}; supported: cloud-{small,medium,large}, load-{small,medium,production}" >&2
    exit 2
    ;;
esac

echo "aws/up.sh: shape=${SHAPE} region=${REGION} release=${RELEASE} node=${NODE_TYPE} desired=${DESIRED} max=${MAX}" >&2

# tfvars is the per-release topology declaration: which managed
# services to provision, their instance sizes, their networking. It
# is written ONCE at first up.sh and reused as-is on every later
# invocation. Reusing it means re-running up.sh after a tier-6
# `run-e2e.sh` (which calls up.sh without WITH_RDS / WITH_ELASTICACHE
# set) does NOT destroy the managed RDS + ElastiCache that an
# earlier tier-7 `run-load.sh` brought up — the infra is treated as
# a whole. To change topology (add managed services, resize, switch
# multi-AZ), run aws/down.sh first; the next up.sh writes a fresh
# tfvars from the current env.
if [[ -f "${TFVARS_FILE}" ]]; then
  echo "aws/up.sh: reusing existing topology declaration ${TFVARS_FILE}" >&2
  echo "  (to change topology, run aws/down.sh first; env flags WITH_RDS / WITH_ELASTICACHE / *_INSTANCE_CLASS apply only on first creation)" >&2
else
  # First-creation env knobs:
  #   WITH_RDS / WITH_ELASTICACHE / WITH_ELASTICACHE_CLUSTER — opt-in
  #     to managed services at first creation. Once on, they stay on
  #     for the lifetime of the release.
  #   RDS_INSTANCE_CLASS / RDS_ALLOCATED_STORAGE_GB / RDS_MULTI_AZ
  #     — RDS size + HA. Tier-7 run-load.sh maps LENNY_LOAD_SCALE
  #     into these so the size aligns with the load envelope.
  #   ELASTICACHE_NODE_TYPE / ELASTICACHE_SHARDS / ELASTICACHE_REPLICAS
  #     — Redis node type and topology.
  #   TEST_CIDR — single CIDR for the caller's home/office IP. Admits
  #     external Postgres + Redis connections so the tier-6 managed-
  #     service tests can run from outside the VPC. Defaults to the
  #     caller's current public IP fetched from checkip.amazonaws.com.
  WITH_RDS="${WITH_RDS:-0}"
  WITH_ELASTICACHE="${WITH_ELASTICACHE:-0}"
  WITH_ELASTICACHE_CLUSTER="${WITH_ELASTICACHE_CLUSTER:-0}"
  RDS_INSTANCE_CLASS="${RDS_INSTANCE_CLASS:-db.t3.micro}"
  RDS_ALLOCATED_STORAGE_GB="${RDS_ALLOCATED_STORAGE_GB:-20}"
  RDS_MULTI_AZ="${RDS_MULTI_AZ:-0}"
  ELASTICACHE_NODE_TYPE="${ELASTICACHE_NODE_TYPE:-cache.t3.micro}"
  ELASTICACHE_REPLICAS="${ELASTICACHE_REPLICAS:-0}"
  ELASTICACHE_SHARDS="${ELASTICACHE_SHARDS:-1}"
  ELASTICACHE_CLUSTER_SHARDS="${ELASTICACHE_CLUSTER_SHARDS:-2}"
  TEST_CIDR="${TEST_CIDR:-$(curl -s https://checkip.amazonaws.com 2>/dev/null | tr -d '[:space:]')/32}"
  if [[ "${TEST_CIDR}" == "/32" ]]; then
    TEST_CIDR=""
  fi
  cidr_array="[]"
  if [[ -n "${TEST_CIDR}" ]]; then
    cidr_array="[\"${TEST_CIDR}\"]"
  fi
  echo "aws/up.sh: writing new topology declaration ${TFVARS_FILE}" >&2
  echo "  rds=${WITH_RDS} (${RDS_INSTANCE_CLASS}, ${RDS_ALLOCATED_STORAGE_GB}GB${RDS_MULTI_AZ:+, multi-AZ})" >&2
  echo "  elasticache=${WITH_ELASTICACHE} (${ELASTICACHE_NODE_TYPE}, shards=${ELASTICACHE_SHARDS}, replicas=${ELASTICACHE_REPLICAS})" >&2
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
  "rds_instance_class": "${RDS_INSTANCE_CLASS}",
  "rds_allocated_storage_gb": ${RDS_ALLOCATED_STORAGE_GB},
  "rds_multi_az":       $([[ "${RDS_MULTI_AZ}" == "1" ]] && echo true || echo false),
  "create_elasticache": $([[ "${WITH_ELASTICACHE}" == "1" ]] && echo true || echo false),
  "elasticache_node_type":               "${ELASTICACHE_NODE_TYPE}",
  "elasticache_num_node_groups":         ${ELASTICACHE_SHARDS},
  "elasticache_replicas_per_node_group": ${ELASTICACHE_REPLICAS},
  "create_elasticache_cluster_mode":     $([[ "${WITH_ELASTICACHE_CLUSTER}" == "1" ]] && echo true || echo false),
  "elasticache_cluster_num_shards":      ${ELASTICACHE_CLUSTER_SHARDS},
  "managed_datastores_test_cidrs":       ${cidr_array}
}
JSON
fi

# Pick terraform if installed; otherwise fall back to opentofu's
# `tofu` binary (drop-in replacement for terraform).
if command -v terraform >/dev/null 2>&1; then
  TF="terraform"
elif command -v tofu >/dev/null 2>&1; then
  TF="tofu"
else
  echo "aws/up.sh: neither terraform nor tofu on PATH; install one of:" >&2
  echo "  brew install hashicorp/tap/terraform" >&2
  echo "  brew install opentofu" >&2
  exit 3
fi

cd "${TF_DIR}"
${TF} init -input=false
${TF} apply -input=false -auto-approve -var-file="${TFVARS_FILE}"

# Sync kubeconfig so kubectl reaches the cluster.
aws eks --region "${REGION}" update-kubeconfig --name "${RELEASE}-eks"

echo "aws/up.sh: cluster ${RELEASE}-eks ready in region ${REGION}" >&2
echo "  artifact_bucket=$(${TF} output -raw artifact_bucket)" >&2
echo "  kms_key_arn=$(${TF} output -raw kms_key_arn)" >&2
echo "  cluster_endpoint=$(${TF} output -raw cluster_endpoint)" >&2
