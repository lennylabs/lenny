#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-action-pins.sh — §20.16 enforces SHA-pinning on
# every external `uses:` reference under .github/workflows/ and
# .github/actions/.
#
# Allowed forms:
#   uses: <owner>/<repo>@<40-char-hex>   # comment naming the version
#   uses: <owner>/<repo>/<sub>@<40-char-hex>   # with sub-action path
#   uses: ./...                          # first-party local path
#
# Disallowed (failing) forms:
#   uses: actions/checkout@v4
#   uses: actions/checkout@main
#   uses: foo/bar@v1.2.3
#   uses: foo/bar@latest
#
# Exit code:
#   0  every external uses: is pinned
#   N  N unpinned references

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

violations=0
report() {
    echo "check-action-pins: $1" >&2
    violations=$((violations + 1))
}

scan_file() {
    local file="$1"
    # grep -n yields LINE:CONTENT pairs so each occurrence gets a
    # distinct location even when the same `uses:` text repeats.
    while IFS=: read -r line_no rest; do
        # Strip the leading "uses:" token from the captured line.
        ref="${rest#*uses:}"
        ref="${ref#"${ref%%[![:space:]]*}"}"
        # Drop comments and trailing whitespace.
        ref="${ref%%#*}"
        ref="${ref%"${ref##*[![:space:]]}"}"
        # Local-path refs (./.github/actions/foo, ./reusable/...)
        # are first-party and don't need pinning.
        if [[ "${ref}" == ./* ]]; then
            continue
        fi
        case "${ref}" in
            "")
                continue
                ;;
            *@*)
                tag="${ref##*@}"
                if [[ "${tag}" =~ ^[0-9a-f]{40}$ ]]; then
                    continue
                fi
                report "${file}:${line_no}: ${ref} — pin to a 40-char SHA (see TESTING.md §20.16)"
                ;;
        esac
    done < <(grep -nE '^\s*-?\s*uses:\s+' "${file}" 2>/dev/null || true)
}

scanned=0
while IFS= read -r f; do
    scan_file "${f}"
    scanned=$((scanned + 1))
done < <(find "${ROOT}/.github/workflows" "${ROOT}/.github/actions" -name '*.yml' -o -name '*.yaml' 2>/dev/null)

if (( violations > 0 )); then
    echo "check-action-pins: ${violations} violation(s) across ${scanned} workflow file(s)" >&2
    echo "See TESTING.md §20.16 for the canonical pin format." >&2
    exit 1
fi

echo "check-action-pins: scanned ${scanned} workflow file(s); every external uses: pinned to SHA"
exit 0
