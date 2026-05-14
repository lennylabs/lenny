#!/usr/bin/env bash
# scripts/cloud/aks/down.sh — tears down the AKS cluster.
set -euo pipefail
SHAPE="${1:-cloud-small}"
cat >&2 <<EOF
aks/down.sh: shape=${SHAPE} — Phase 13+ deliverable. When implemented:
  terraform destroy -chdir=deploy/terraform/cloud/aks -var-file=${SHAPE}.tfvars
EOF
exit 0
