#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/setup-cluster.sh — provisions a Kind cluster with the Lenny test
# profile. Idempotent.
#
# Cluster shape (TESTING.md §10):
#   - 1 control-plane node
#   - 2 worker nodes
#   - Calico CNI (NetworkPolicy enforcement)
#   - cert-manager
#   - metrics-server
#   - RuntimeClass registration for runc (gVisor is host-dependent)
#
# Phase 0 implementation: the Kind config and the add-on manifests are stubs
# referenced by name; the script verifies Docker is running, names the cluster,
# and prints the manual steps until tests/testinfra/kind/ ships in Phase 3.
# After Phase 3, this script becomes the canonical provisioning entry point.

set -euo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

CLUSTER_NAME="lenny-test"
KUBECONFIG_PATH=""
MODE="up"

usage() {
  cat <<'EOF'
Usage: setup-cluster.sh [flags]

Flags:
  (default)         Provision or recreate the test cluster.
  --reuse           Use an existing cluster if present; otherwise create one.
  --delete          Tear down the cluster.
  --kubeconfig <p>  Write kubeconfig to <p> (default: $KUBECONFIG or ~/.kube/config).
  --name <n>        Cluster name (default: lenny-test).
  --help            This message.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --reuse)        MODE="reuse"; shift ;;
    --delete)       MODE="delete"; shift ;;
    --kubeconfig)   KUBECONFIG_PATH="$2"; shift 2 ;;
    --name)         CLUSTER_NAME="$2"; shift 2 ;;
    --help|-h)      usage; exit 0 ;;
    *)              echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Sanity: required tools.
for cmd in docker kind kubectl helm; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    lenny_log_err "$cmd is not on PATH. Run scripts/setup-dev.sh --include kubernetes."
    exit 2
  fi
done
if ! docker info >/dev/null 2>&1; then
  lenny_log_err "docker is not running. Start Docker Desktop / Colima / OrbStack first."
  exit 2
fi

case "$MODE" in
  delete)
    lenny_log_info "deleting cluster '$CLUSTER_NAME'"
    kind delete cluster --name "$CLUSTER_NAME" || true
    lenny_log_ok "deleted"
    exit 0
    ;;
  reuse)
    if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
      lenny_log_ok "cluster '$CLUSTER_NAME' already running; reusing"
      exit 0
    fi
    ;;
esac

# Recreate semantics: delete-if-present, then create.
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  lenny_log_info "cluster '$CLUSTER_NAME' exists; deleting before recreating"
  kind delete cluster --name "$CLUSTER_NAME"
fi

KIND_CONFIG="$SCRIPT_DIR/../tests/testinfra/kind/cluster.yaml"
if [[ ! -f "$KIND_CONFIG" ]]; then
  lenny_log_warn "$KIND_CONFIG is a Phase 3 deliverable and not yet present."
  lenny_log_warn "Creating a default Kind cluster instead. Phase 3 replaces this with the canonical config."
  kind create cluster --name "$CLUSTER_NAME"
else
  kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG"
fi

# Apply add-ons (Phase 3 wires the real manifests; Phase 0 only places the
# pointer so the script does not silently misrepresent its state).
ADDONS_DIR="$SCRIPT_DIR/../tests/testinfra/kind/addons"
if [[ -d "$ADDONS_DIR" ]]; then
  for f in "$ADDONS_DIR"/*.yaml; do
    [[ -f "$f" ]] || continue
    lenny_log_info "applying $(basename "$f")"
    kubectl apply -f "$f"
  done
else
  lenny_log_warn "$ADDONS_DIR not present (Phase 3 deliverable)."
  lenny_log_warn "Calico, cert-manager, and metrics-server are NOT installed by this Phase 0 script."
fi

if [[ -n "$KUBECONFIG_PATH" ]]; then
  kind get kubeconfig --name "$CLUSTER_NAME" > "$KUBECONFIG_PATH"
  lenny_log_ok "kubeconfig written to $KUBECONFIG_PATH"
fi

lenny_log_ok "cluster '$CLUSTER_NAME' ready"
kubectl --context "kind-$CLUSTER_NAME" get nodes
