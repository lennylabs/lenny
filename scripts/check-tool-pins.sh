#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-tool-pins.sh — supply-chain hygiene for tool installs.
# Companion to scripts/check-action-pins.sh.
#
# Flags `go install <path>@latest` in workflow YAMLs because every
# CI run otherwise picks up whatever upstream has tagged most
# recently. The corresponding rule is in TESTING.md §20.16 (action
# pinning discipline, extended to tool installs).
#
# Allowed forms:
#   go install <path>@<vX.Y.Z>
#   go install <path>@<40-char-hex>
#
# Disallowed:
#   go install <path>@latest
#   go install <path>@main
#   go install <path>@master
#
# `setup-dev.sh` is exempt; local developer setup may take the
# upstream HEAD because the script is interactive and operators run
# it intentionally.
#
# Exit code:
#   0  no offenders found
#   1  offenders found

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

violations=0
report() {
    echo "check-tool-pins: $1" >&2
    violations=$((violations + 1))
}

scan_file() {
    local file="$1"
    while IFS=: read -r line_no rest; do
        report "${file}:${line_no}: ${rest# } — pin to @vX.Y.Z (see TESTING.md §20.16)"
    done < <(grep -nE 'go install [^[:space:]]+@(latest|main|master)\b' "${file}" 2>/dev/null || true)
}

scanned=0
while IFS= read -r f; do
    scan_file "${f}"
    scanned=$((scanned + 1))
done < <(find "${ROOT}/.github/workflows" -name '*.yml' -o -name '*.yaml' 2>/dev/null)

if (( violations > 0 )); then
    echo "check-tool-pins: ${violations} violation(s) across ${scanned} workflow file(s)" >&2
    echo "See TESTING.md §20.16. setup-dev.sh is exempt; CI is not." >&2
    exit 1
fi

echo "check-tool-pins: scanned ${scanned} workflow file(s); every go install pinned to a version"
exit 0
