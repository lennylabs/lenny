#!/usr/bin/env bash
# scripts/cloud/eks/down.sh — tears down the EKS cluster.
set -euo pipefail
SHAPE="${1:-cloud-small}"
cat >&2 <<EOF
eks/down.sh: shape=${SHAPE} — Phase 13+ deliverable. When implemented:
  terraform destroy -chdir=deploy/terraform/cloud/eks -var-file=${SHAPE}.tfvars
EOF
exit 0
