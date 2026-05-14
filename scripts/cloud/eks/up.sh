#!/usr/bin/env bash
# scripts/cloud/eks/up.sh — brings up an EKS cluster for tier-6.
set -euo pipefail
SHAPE="${1:-cloud-small}"
cat >&2 <<EOF
eks/up.sh: shape=${SHAPE} — Phase 13+ deliverable; deploy/terraform/cloud/eks/ not yet present.
When implemented, runs terraform apply for the EKS shape with the
chosen runc / Firecracker / gVisor node-pool variant.
EOF
exit 0
