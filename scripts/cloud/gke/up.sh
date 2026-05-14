#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gke/up.sh — brings up a GKE cluster for tier-6 tests.
#
# Usage:
#   scripts/cloud/gke/up.sh <shape>
#
# Where <shape> is one of:
#   cloud-small    3-node, runc only, no sandbox node pool
#   cloud-sandbox  3-node + gVisor sandbox node pool
#
# Today this script is a placeholder. The real bring-up wires
# Terraform under deploy/terraform/cloud/gke/. When that lands, this
# wrapper invokes terraform apply with the matching shape's variable
# file.

set -euo pipefail

SHAPE="${1:-cloud-small}"

case "${SHAPE}" in
  cloud-small|cloud-sandbox)
    ;;
  *)
    echo "gke/up.sh: unknown shape ${SHAPE}; expected cloud-small | cloud-sandbox" >&2
    exit 2
    ;;
esac

cat >&2 <<EOF
gke/up.sh: shape=${SHAPE} — Phase 13+ deliverable; deploy/terraform/cloud/gke/ not yet present.

When implemented, this script will:
  1. terraform init -chdir=deploy/terraform/cloud/gke
  2. terraform apply -chdir=deploy/terraform/cloud/gke -var-file=${SHAPE}.tfvars
  3. gcloud container clusters get-credentials lenny-${SHAPE}
  4. kubectl apply -f compose/kind/lenny-namespace.yaml  (or equivalent)

Today the tier-6 tests detect the missing chart and skip via cloud.SkipUnlessAvailable.
EOF
exit 0
