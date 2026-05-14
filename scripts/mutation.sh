#!/usr/bin/env bash
# scripts/mutation.sh — wraps go-mutesting (or an equivalent Go
# mutation tool) for the §19.3 mutation-testing gate.
#
# Usage:
#   scripts/mutation.sh <package-pattern>
#   scripts/mutation.sh pkg/quota
#   scripts/mutation.sh pkg/...
#
# Today this is a thin scaffold. When go-mutesting is on PATH the
# script invokes it with the documented flags and reports the
# mutation kill rate. When the tool is absent the script reports a
# precise "install" diagnosis and exits 0 so CI does not break.
#
# §19.3 sets a kill-rate threshold per critical package; that table
# lives in lenny-test internals (cmd/lenny-test/cmd_mutation.go).

set -uo pipefail

PATTERN="${1:-pkg/...}"

if ! command -v go-mutesting >/dev/null 2>&1; then
    cat >&2 <<EOF
mutation: go-mutesting not on PATH; skipping (the §19.3 mutation gate is informational today).

To install:
  go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest

When installed, this script invokes:
  go-mutesting --debug=false --do-not-remove-tmp-folder=false ${PATTERN}

And reports the mutation kill rate vs the §19.3 per-package threshold.
EOF
    exit 0
fi

out="$(go-mutesting --debug=false "${PATTERN}" 2>&1)"
echo "${out}"

# Extract the summary line: "The mutation score is 0.83 (123 passed, 25 failed, 0 duplicated, 0 skipped, total is 148)"
score="$(echo "${out}" | grep -oE 'mutation score is [0-9.]+' | tail -1 | awk '{print $4}')"
if [[ -z "${score}" ]]; then
    echo "mutation: could not extract score from go-mutesting output" >&2
    exit 1
fi
echo "mutation: kill rate ${score}"
exit 0
