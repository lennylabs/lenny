#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-adr-catalog.sh — verifies the ADR catalog is intact.
#
# Checks:
#   1. Every ADR file at docs/adr/NNNN-*.md has a matching entry in
#      docs/adr/index.md.
#   2. Conversely, every index link target refers to a file that exists.
#   3. The catalog numbering in index.md has no gaps and no duplicates.
#   4. Every catalog entry either has a file (when written) or a spec
#      reference (when "Planned"). "Planned" entries reserve a number
#      without a file, per the model documented in index.md.
#   5. Every ADR file appears in the catalog.
#
# Exit code:
#   0  success
#   N  N violations (file paths and line numbers on stderr)
#
# Usage:
#   scripts/check-adr-catalog.sh [adr-dir]
#   Default adr-dir is docs/adr/ relative to repo root.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADR_DIR="${1:-${ROOT}/docs/adr}"
INDEX="${ADR_DIR}/index.md"

violations=0
report() {
    echo "check-adr-catalog: $1" >&2
    violations=$((violations + 1))
}

if [[ ! -d "${ADR_DIR}" ]]; then
    echo "check-adr-catalog: ${ADR_DIR} does not exist; nothing to validate"
    exit 0
fi

# Collect ADR files. Pattern: NNNN-<slug>.md, excluding index.md.
adr_files=()
while IFS= read -r f; do
    [[ -n "$f" ]] && adr_files+=("$f")
done < <(find "${ADR_DIR}" -maxdepth 1 -type f -name '[0-9][0-9][0-9][0-9]-*.md' | sort)

if [[ ${#adr_files[@]} -eq 0 ]]; then
    echo "check-adr-catalog: no ADRs found in ${ADR_DIR}; nothing to validate"
    exit 0
fi

if [[ ! -f "${INDEX}" ]]; then
    report "missing ${INDEX}"
    exit "${violations}"
fi

# Pass 1: every ADR file is referenced by index.md. Accept either
# the .md or .html extension (Jekyll renders .md to .html so the
# catalog often links the latter).
for f in "${adr_files[@]}"; do
    name="$(basename "${f}")"
    stem="${name%.md}"
    if ! grep -qE "${stem}\.(md|html)" "${INDEX}"; then
        report "ADR ${name} not referenced from ${INDEX}"
    fi
done

# Pass 2: every link target in index.md resolves to an extant file.
# A link target looks like (NNNN-slug.md|html) — extract those and
# verify the .md sibling exists.
while IFS= read -r target; do
    stem="${target%.md}"
    stem="${stem%.html}"
    if [[ "${stem}" == "index" ]]; then
        continue
    fi
    if [[ ! -f "${ADR_DIR}/${stem}.md" ]]; then
        report "index.md references missing file ${stem}.md"
    fi
done < <(grep -oE '\([0-9]{4}-[a-z0-9-]+\.(md|html)\)' "${INDEX}" | tr -d '()' | sort -u)

# File-number set (colon-delimited) + duplicate-file check.
file_num_list=""
for f in "${adr_files[@]}"; do
    n="$(basename "${f}" | cut -c1-4)"
    if [[ "${file_num_list}" == *":${n}:"* ]]; then
        report "duplicate ADR file number ${n}"
        continue
    fi
    file_num_list="${file_num_list}:${n}:"
done

# Pass 3 + 4: catalog continuity. A catalog row is a Markdown table
# row in index.md that names an ADR number and a status. "Planned"
# rows reserve a number without a file and must carry a spec
# reference; every other status requires a matching NNNN-*.md file.
# The catalog numbering must be gap-free with no duplicates.
cat_num_list=""
cat_nums=""
while IFS= read -r line; do
    case "${line}" in
        \|*) ;;
        *) continue ;;
    esac
    if [[ "${line}" =~ ADR-([0-9]{4}) ]]; then
        num="${BASH_REMATCH[1]}"
    else
        continue
    fi
    status=""
    for s in Planned Proposed Accepted Deprecated Superseded; do
        case "${line}" in
            *"${s}"*) status="${s}"; break ;;
        esac
    done
    [[ -n "${status}" ]] || continue
    if [[ "${cat_num_list}" == *":${num}:"* ]]; then
        report "duplicate ADR number ${num} in catalog"
        continue
    fi
    cat_num_list="${cat_num_list}:${num}:"
    cat_nums="${cat_nums} ${num}"
    if [[ "${status}" == "Planned" ]]; then
        case "${line}" in
            *'[§'*) ;;
            *) report "ADR-${num} is Planned but has no spec reference in the catalog row" ;;
        esac
    elif [[ "${file_num_list}" != *":${num}:"* ]]; then
        report "ADR-${num} is ${status} in the catalog but has no ${num}-*.md file"
    fi
done < "${INDEX}"

# Pass 5: every ADR file appears in the catalog.
for f in "${adr_files[@]}"; do
    n="$(basename "${f}" | cut -c1-4)"
    if [[ "${cat_num_list}" != *":${n}:"* ]]; then
        report "ADR file ${n}-*.md has no catalog entry in ${INDEX}"
    fi
done

# Catalog numbering — no gaps.
prev=""
for n in $(printf '%s\n' ${cat_nums} | sort); do
    if [[ -n "${prev}" ]]; then
        expected=$(printf '%04d' $((10#${prev} + 1)))
        if [[ "${n}" != "${expected}" ]]; then
            report "ADR catalog gap: after ${prev} expected ${expected} but found ${n}"
        fi
    fi
    prev="${n}"
done

if (( violations > 0 )); then
    echo "check-adr-catalog: ${violations} violation(s)" >&2
    exit "${violations}"
fi

echo "check-adr-catalog: ${#adr_files[@]} ADR(s) present; catalog intact"
exit 0
