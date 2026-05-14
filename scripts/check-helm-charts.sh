#!/usr/bin/env bash
# scripts/check-helm-charts.sh — runs `helm lint` and `conftest test`
# against the Helm chart at charts/lenny/.
#
# Static tier gate per TESTING.md §12.0 #5–6. When charts/lenny/ does
# not yet exist (the chart is a Phase 3 deliverable) the script
# reports and skips. When helm or conftest aren't on PATH it also
# skips with a diagnosis.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="${ROOT}/charts/lenny"
POLICY="${CHART}/policy"

if [[ ! -d "${CHART}" ]]; then
    echo "check-helm-charts: ${CHART} does not exist; chart is a Phase 3 deliverable"
    exit 0
fi

failures=0

if command -v helm >/dev/null 2>&1; then
    if ! helm lint "${CHART}"; then
        failures=$((failures + 1))
        echo "check-helm-charts: helm lint failed" >&2
    fi
else
    echo "check-helm-charts: helm not on PATH; skipping helm lint"
fi

if command -v conftest >/dev/null 2>&1 && [[ -d "${POLICY}" ]]; then
    values="${ROOT}/tests/testinfra/kind/values.yaml"
    if [[ ! -f "${values}" ]]; then
        echo "check-helm-charts: no test values at ${values}; skipping conftest"
    else
        if ! helm template "${CHART}" --values "${values}" | conftest test --policy "${POLICY}" -; then
            failures=$((failures + 1))
            echo "check-helm-charts: conftest policy check failed" >&2
        fi
    fi
elif [[ -d "${POLICY}" ]]; then
    echo "check-helm-charts: conftest not on PATH; skipping policy check"
fi

if (( failures > 0 )); then
    exit 1
fi

echo "check-helm-charts: chart lint + policy clean"
exit 0
