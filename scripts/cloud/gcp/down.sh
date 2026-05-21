#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/cloud/gke/down.sh — tears down the GKE cluster brought up
# by up.sh.
#
# Usage:
#   scripts/cloud/gke/down.sh <shape>

set -euo pipefail

SHAPE="${1:-cloud-small}"

cat >&2 <<EOF
gke/down.sh: shape=${SHAPE} — Phase 13+ deliverable. When implemented:
  1. terraform destroy -chdir=deploy/terraform/cloud/gke -var-file=${SHAPE}.tfvars
  2. gcloud container clusters delete lenny-${SHAPE}
EOF
exit 0
