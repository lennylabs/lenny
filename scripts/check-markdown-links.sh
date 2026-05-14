#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-markdown-links.sh — runs markdown-link-check over
# docs/ and spec/ Markdown files. Wrapper script invoked by the
# static tier; gracefully skips when the tool is absent.
#
# Configuration lives at .markdown-link-check.json relative to repo
# root. The script honors LENNY_LINK_CHECK_VERBOSE=1 to switch off
# the quiet flag for debugging.
#
# Exit code:
#   0  all links valid (or skipped because markdown-link-check not present)
#   1  one or more broken links

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v markdown-link-check >/dev/null 2>&1; then
    echo "check-markdown-links: markdown-link-check not on PATH; skipping"
    echo "  install with: npm install -g markdown-link-check@3.12.2"
    exit 0
fi

CONFIG="${ROOT}/.markdown-link-check.json"
quiet_flag="-q"
if [[ "${LENNY_LINK_CHECK_VERBOSE:-0}" == "1" ]]; then
    quiet_flag=""
fi

failures=0
for dir in docs spec; do
    full="${ROOT}/${dir}"
    if [[ ! -d "${full}" ]]; then
        continue
    fi
    while IFS= read -r f; do
        if ! markdown-link-check "${f}" ${quiet_flag} --config "${CONFIG}" 2>/dev/null; then
            failures=$((failures + 1))
            echo "check-markdown-links: broken link(s) in ${f}" >&2
        fi
    done < <(find "${full}" -type f -name '*.md' -not -path '*/node_modules/*')
done

if (( failures > 0 )); then
    echo "check-markdown-links: ${failures} file(s) with broken links" >&2
    exit 1
fi

echo "check-markdown-links: all docs/ and spec/ markdown links resolve"
exit 0
