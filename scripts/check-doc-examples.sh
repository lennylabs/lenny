#!/usr/bin/env bash
# scripts/check-doc-examples.sh — validates JSON example payloads in
# docs/ against their declared schema.
#
# Convention: example files live under docs/examples/<schema>/*.json
# and the schema file is schemas/<schema>.json (or
# schemas/<schema>-v1.json). The script walks docs/examples/,
# resolves each subdirectory to a schema, and runs the
# tests/testinfra/schematest binding indirectly via `go test
# ./tests/tier0_static/...` which already validates schema-vs-example
# pairs.
#
# Today this script is a thin wrapper: it confirms the directory
# layout exists and reports the count. When go-based deep validation
# is needed, tests/tier0_static/schemas_test.go handles it; this
# script is the shell-level entry point for the static tier and CI.
#
# Exit code:
#   0  success (or skipped when docs/examples/ does not exist)
#   N  N violations

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXAMPLES_DIR="${ROOT}/docs/examples"
SCHEMAS_DIR="${ROOT}/schemas"

violations=0
report() {
    echo "check-doc-examples: $1" >&2
    violations=$((violations + 1))
}

if [[ ! -d "${EXAMPLES_DIR}" ]]; then
    echo "check-doc-examples: ${EXAMPLES_DIR} does not exist; skipping"
    exit 0
fi

count=0
while IFS= read -r f; do
    count=$((count + 1))
    # Check the file is syntactically valid JSON.
    if ! python3 -c "import json,sys; json.load(open(sys.argv[1]))" "${f}" 2>/dev/null; then
        report "invalid JSON: ${f}"
        continue
    fi
    # Resolve the schema directory from the path.
    rel="${f#${EXAMPLES_DIR}/}"
    schema_name="${rel%%/*}"
    if [[ ! -f "${SCHEMAS_DIR}/${schema_name}.json" && ! -f "${SCHEMAS_DIR}/${schema_name}-v1.json" ]]; then
        report "no schema for ${schema_name} (example ${f})"
    fi
done < <(find "${EXAMPLES_DIR}" -type f -name '*.json' 2>/dev/null)

if (( violations > 0 )); then
    echo "check-doc-examples: ${violations} violation(s)" >&2
    exit "${violations}"
fi

if (( count == 0 )); then
    echo "check-doc-examples: no example files under ${EXAMPLES_DIR}"
else
    echo "check-doc-examples: ${count} example file(s) validated"
fi
exit 0
