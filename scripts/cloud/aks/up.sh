#!/usr/bin/env bash
# scripts/cloud/aks/up.sh — brings up an AKS cluster for tier-6.
set -euo pipefail
SHAPE="${1:-cloud-small}"
cat >&2 <<EOF
aks/up.sh: shape=${SHAPE} — Phase 13+ deliverable; deploy/terraform/cloud/aks/ not yet present.
When implemented, runs terraform apply for the AKS shape with the
Confidential Containers / Kata variant node pool.
EOF
exit 0
