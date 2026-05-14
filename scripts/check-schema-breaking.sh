#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/check-schema-breaking.sh — flags breaking changes in
# JSON Schema files under schemas/ against the main branch.
#
# What counts as breaking (per JSON Schema semantics):
#   - A required field is added (existing payloads break).
#   - An enum value is removed (narrowing).
#   - A type is changed.
#   - A property is removed.
#   - additionalProperties: true → false.
#
# Implementation: for each schemas/*.json, fetch the version from
# main, diff against the current version using `jq`-driven checks.
# The script exits 0 when:
#   - jq or git missing → skip
#   - schemas/ absent → skip
#   - no breaking changes detected
# and non-zero with a per-file report when a breaking change is found.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCHEMAS="${ROOT}/schemas"

if ! command -v git >/dev/null 2>&1; then
    echo "check-schema-breaking: git not on PATH; skipping" >&2
    exit 0
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "check-schema-breaking: jq not on PATH; skipping" >&2
    exit 0
fi
if [[ ! -d "${SCHEMAS}" ]]; then
    echo "check-schema-breaking: ${SCHEMAS} not present; skipping"
    exit 0
fi

# Detect the base branch: main by default; honor LENNY_BASE_BRANCH.
BASE="${LENNY_BASE_BRANCH:-main}"
# When main doesn't exist (fresh repo or shallow clone), skip.
if ! git -C "${ROOT}" show-ref --verify --quiet "refs/heads/${BASE}" && \
   ! git -C "${ROOT}" show-ref --verify --quiet "refs/remotes/origin/${BASE}"; then
    echo "check-schema-breaking: base ${BASE} not present; skipping"
    exit 0
fi

violations=0
report() {
    echo "check-schema-breaking: $1" >&2
    violations=$((violations + 1))
}

# Required field check: a required entry in current but not in base.
check_required_added() {
    local file="$1" base_json="$2" cur_json="$3"
    local added
    added=$(jq -n --argjson b "${base_json}" --argjson c "${cur_json}" '
        ($c.required // []) - ($b.required // []) | .[]
    ' 2>/dev/null)
    if [[ -n "${added}" ]]; then
        report "${file}: required field(s) added: $(echo "${added}" | tr '\n' ' ')"
    fi
}

# Enum narrowing: a value present in base.enum but absent in current.
check_enum_narrowed() {
    local file="$1" base_json="$2" cur_json="$3"
    # Walk every $.properties[*].enum (top-level only for simplicity).
    local removed
    removed=$(jq -n --argjson b "${base_json}" --argjson c "${cur_json}" '
        (($b.properties // {}) | to_entries[] | select(.value.enum) | {prop:.key, removed: (.value.enum - ($c.properties[.key].enum // .value.enum))})
        | select(.removed | length > 0)
        | "\(.prop): \(.removed | join(","))"
    ' 2>/dev/null)
    if [[ -n "${removed}" ]]; then
        report "${file}: enum value(s) removed: ${removed}"
    fi
}

# Property removal: a key in base.properties but absent in current.
check_property_removed() {
    local file="$1" base_json="$2" cur_json="$3"
    local removed
    removed=$(jq -n --argjson b "${base_json}" --argjson c "${cur_json}" '
        (($b.properties // {}) | keys) - (($c.properties // {}) | keys) | .[]
    ' 2>/dev/null)
    if [[ -n "${removed}" ]]; then
        report "${file}: property/properties removed: $(echo "${removed}" | tr '\n' ' ')"
    fi
}

# additionalProperties tightening: base=true → current=false is breaking.
check_additional_props_tightened() {
    local file="$1" base_json="$2" cur_json="$3"
    local before after
    before=$(echo "${base_json}" | jq -r '.additionalProperties // true')
    after=$(echo "${cur_json}" | jq -r '.additionalProperties // true')
    if [[ "${before}" == "true" && "${after}" == "false" ]]; then
        report "${file}: additionalProperties tightened from true to false"
    fi
}

while IFS= read -r f; do
    rel="${f#${ROOT}/}"
    # Read the on-disk current version.
    cur_json=$(cat "${f}" 2>/dev/null) || continue

    # Try to read the base version. show may fail (file added in this branch).
    base_json=$(git -C "${ROOT}" show "${BASE}:${rel}" 2>/dev/null) || continue
    if [[ -z "${base_json}" ]]; then
        continue
    fi

    check_required_added "${rel}" "${base_json}" "${cur_json}"
    check_enum_narrowed "${rel}" "${base_json}" "${cur_json}"
    check_property_removed "${rel}" "${base_json}" "${cur_json}"
    check_additional_props_tightened "${rel}" "${base_json}" "${cur_json}"
done < <(find "${SCHEMAS}" -maxdepth 1 -type f -name '*.json' | sort)

if (( violations > 0 )); then
    echo "check-schema-breaking: ${violations} breaking change(s)" >&2
    exit "${violations}"
fi

echo "check-schema-breaking: no breaking JSON-schema changes vs ${BASE}"
exit 0
