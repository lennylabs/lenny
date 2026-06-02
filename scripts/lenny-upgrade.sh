#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# scripts/lenny-upgrade.sh — the §10.5 CRD-aware upgrade driver.
#
# Helm does not update CRDs on `helm upgrade` (a known Helm limitation):
# stale CRDs after an upgrade silently strip fields the new binaries
# write. This script runs the §10.5 "CRD upgrade procedure" so a release
# that changes CRD schemas applies them in the correct order:
#
#   1. Preflight — assert the installed CRD schema-version is current.
#   2. Diff CRDs — show what `kubectl apply` will change (confirm unless
#      --non-interactive).
#   3. Apply CRDs — `kubectl apply -f charts/lenny/crds/`.
#   4. Wait for CRD establishment — each updated CRD reaches Established.
#   5. helm upgrade — controllers re-validate the CRD schema-version on
#      startup and refuse to start if any CRD is still stale.
#
# Also available as `make upgrade RELEASE=<version> NAMESPACE=<ns> \
#   VALUES=<values.yaml> [NON_INTERACTIVE=true]`.
#
# spec: §10.5 "CRD upgrade procedure" (lines 439-462). F-10.5.4.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CRD_DIR="${ROOT}/charts/lenny/crds"
CHART="${ROOT}/charts/lenny"

RELEASE=""
NAMESPACE="lenny-system"
VALUES=""
RELEASE_NAME="lenny"
NON_INTERACTIVE="false"

usage() {
    cat >&2 <<'EOF'
usage: scripts/lenny-upgrade.sh --release <version> --values <values.yaml> [options]

Required:
  --release <version>      Target chart/app version (recorded; passed to helm --version)
  --values <values.yaml>   Helm values file

Options:
  --namespace <ns>         Release namespace (default: lenny-system)
  --release-name <name>    Helm release name (default: lenny)
  --non-interactive        Skip the CRD-diff confirmation prompt (CI / GitOps)
  -h, --help               Print this message
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --release)        RELEASE="$2"; shift 2 ;;
        --namespace)      NAMESPACE="$2"; shift 2 ;;
        --values)         VALUES="$2"; shift 2 ;;
        --release-name)   RELEASE_NAME="$2"; shift 2 ;;
        --non-interactive) NON_INTERACTIVE="true"; shift ;;
        -h|--help)        usage; exit 0 ;;
        *) echo "lenny-upgrade: unknown argument: $1" >&2; usage; exit 2 ;;
    esac
done

if [[ -z "${RELEASE}" ]]; then
    echo "lenny-upgrade: --release <version> is required" >&2
    usage
    exit 2
fi
if [[ -z "${VALUES}" ]]; then
    echo "lenny-upgrade: --values <values.yaml> is required" >&2
    usage
    exit 2
fi
if [[ ! -f "${VALUES}" ]]; then
    echo "lenny-upgrade: values file not found: ${VALUES}" >&2
    exit 2
fi

for bin in kubectl helm; do
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "lenny-upgrade: required tool not found on PATH: ${bin}" >&2
        exit 2
    fi
done

echo "lenny-upgrade: upgrading release '${RELEASE_NAME}' to ${RELEASE} in namespace '${NAMESPACE}'"

# Step 1: preflight CRD-currency assertion (§10.5 line 443). Prefer the
# interactive lenny-ctl preflight; fall back to the lenny-preflight Job
# binary, which the spec names as the equivalent. The check fails closed,
# and set -e aborts the upgrade on a non-zero exit.
echo "lenny-upgrade: [1/5] preflight — asserting CRD schema-version currency"
if command -v lenny-ctl >/dev/null 2>&1; then
    lenny-ctl preflight --config "${VALUES}"
elif command -v lenny-preflight >/dev/null 2>&1; then
    lenny-preflight --namespace "${NAMESPACE}"
else
    echo "lenny-upgrade: neither lenny-ctl nor lenny-preflight is on PATH; cannot run the §10.5 CRD-currency preflight" >&2
    exit 2
fi

# Step 2: diff the target CRDs against the installed ones. kubectl diff
# exits 1 when a diff exists, which is expected, so the exit status is
# not treated as a failure here.
echo "lenny-upgrade: [2/5] diff CRDs in ${CRD_DIR}"
kubectl diff -f "${CRD_DIR}" || true
if [[ "${NON_INTERACTIVE}" != "true" ]]; then
    read -r -p "lenny-upgrade: apply the CRD changes above? [y/N] " reply
    case "${reply}" in
        y|Y|yes|YES) ;;
        *) echo "lenny-upgrade: aborted by operator before applying CRDs" >&2; exit 1 ;;
    esac
fi

# Step 3: apply the CRDs before any controller binary changes.
echo "lenny-upgrade: [3/5] apply CRDs"
kubectl apply -f "${CRD_DIR}"

# Step 4: wait for each updated CRD to reach Established so the API server
# has ingested the new schema before controllers use it.
echo "lenny-upgrade: [4/5] wait for CRD establishment"
while IFS= read -r crd_file; do
    name="$(kubectl apply -f "${crd_file}" --dry-run=client -o jsonpath='{.metadata.name}' 2>/dev/null || true)"
    if [[ -z "${name}" ]]; then
        continue
    fi
    echo "lenny-upgrade:   wait crd/${name} Established"
    kubectl wait --for=condition=Established "crd/${name}" --timeout=60s
done < <(find "${CRD_DIR}" -type f -name '*.yaml' | sort)

# Step 5: run the Helm upgrade. Controllers re-check the CRD schema-version
# on startup and CrashLoopBackOff if any CRD is still stale, providing the
# hard stop against a partial upgrade.
echo "lenny-upgrade: [5/5] helm upgrade"
helm upgrade "${RELEASE_NAME}" "${CHART}" \
    --namespace "${NAMESPACE}" \
    --values "${VALUES}" \
    --version "${RELEASE}"

echo "lenny-upgrade: upgrade complete"
