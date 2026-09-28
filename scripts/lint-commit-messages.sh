#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-commit-messages.sh — rejects commit subjects that carry a
# proposal's scaffolding labels.
#
# A proposal under proposals/ numbers its own parts: build steps (S<n>,
# S-<X><n>), change and deliverable ids (CODE-<n>, TEST-<n>, SPEC-<X>,
# C-<X><n>), decision ids (D<n>, RES-<n>), and review passes (Pass <n>).
# Those labels name parts of the proposal document rather than the
# shipped system, and a subject such as "NNNN S<n>: ..." means nothing
# to a reader of the first-parent history once the proposal is closed. A subject names the
# behavior and the spec section instead (see the harness commit
# conventions and .claude/rules/code-best-practices.md).
#
# Scope:
#   - Only subjects are checked. A body may quote a proposal path or a
#     BUILD-GAPS finding id for traceability.
#   - A commit whose every changed path sits under proposals/ is exempt.
#     The proposal pipeline records its own drafting, review, and ticks
#     there, and those records use the proposal's labels by design.
#     An empty commit changes no path and is not exempt.
#   - The default range starts at FLOOR, the commit that introduced this
#     lint. Commits before it are historical and are not rewritten. When
#     FLOOR is not an ancestor of HEAD (another line of history, or a
#     shallow clone), the default run checks nothing and says so.
#
# The bare subject token `S3` is accepted because it names the object
# store; the same label after a proposal number or the word "step" is
# still rejected.
#
# Usage:
#   scripts/lint-commit-messages.sh [rev-range]
#
# Exit code:
#   0  no violations
#   1  one or more violations (each on stderr)

set -euo pipefail

FLOOR="c1b11a552"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

range="${1:-}"
if [[ -z "${range}" ]]; then
    if ! git merge-base --is-ancestor "${FLOOR}" HEAD 2>/dev/null; then
        echo "lint-commit-messages: floor ${FLOOR} is not an ancestor of HEAD; nothing to check"
        exit 0
    fi
    range="${FLOOR}..HEAD"
fi

# Each pattern is an extended regex matched against the subject.
patterns=(
    '(^|[^0-9])[0-9]{4}:? S(-[A-Z])?[0-9]+([^0-9]|$)'
    '(^|[^A-Za-z])[Ss]teps? S(-[A-Z])?[0-9]+([^0-9]|$)'
    '(^|[^A-Za-z0-9-])S-[A-Z][0-9]+([^0-9]|$)'
    '(^|[^A-Za-z0-9-])S([0-9]|[0-9]{2})([^0-9A-Za-z]|$)'
    '(^|[^A-Za-z0-9-])(CODE|TEST|SPEC)-([0-9]+|[A-Z][0-9]*)([^A-Za-z0-9]|$)'
    '(^|[^A-Za-z0-9-])C-[A-Z][0-9]+([^0-9]|$)'
    '(^|[^A-Za-z0-9-])RES-[0-9]+([^0-9]|$)'
    '(^|[^A-Za-z0-9-])D[0-9]{1,2}([^0-9A-Za-z]|$)'
    '(^|[^A-Za-z])Pass [0-9]+([^0-9]|$)'
)
# BARE_STEP is the index of the bare S<n> pattern above.
BARE_STEP=3

# offending_label prints the first scaffolding label in a subject, or
# nothing when the subject is clean. The bare-step pattern runs against
# the subject with each standalone S3 token removed.
offending_label() {
    local subject="$1" p text hit i
    for i in "${!patterns[@]}"; do
        p="${patterns[i]}"
        text="${subject}"
        if ((i == BARE_STEP)); then
            text="$(sed -E 's/(^|[^A-Za-z0-9-])S3([^0-9A-Za-z]|$)/\1\2/g' <<<"${subject}")"
        fi
        hit="$(grep -oE -- "${p}" <<<"${text}" | head -n1 || true)"
        [[ -n "${hit}" ]] || continue
        sed -E 's/^[^A-Za-z0-9]+//; s/[^A-Za-z0-9]+$//' <<<"${hit}"
        return 0
    done
}

# proposals_only reports whether a commit changes at least one path and
# every changed path is under proposals/.
proposals_only() {
    local sha="$1" paths
    paths="$(git diff-tree --no-commit-id --name-only -r -m --root "${sha}")"
    [[ -n "${paths}" ]] || return 1
    ! grep -qv '^proposals/' <<<"${paths}"
}

violations=0
while IFS=$'\t' read -r sha subject; do
    [[ -n "${sha}" ]] || continue
    label="$(offending_label "${subject}")"
    [[ -n "${label}" ]] || continue
    proposals_only "${sha}" && continue
    echo "lint-commit-messages: ${sha:0:9} subject carries proposal label '${label}': ${subject}" >&2
    violations=$((violations + 1))
done < <(git log --format='%H%x09%s' "${range}")

if ((violations > 0)); then
    echo "lint-commit-messages: ${violations} subject(s) name a proposal's scaffolding label; name the behavior and the spec section instead" >&2
    exit 1
fi
echo "lint-commit-messages: ok (${range})"
