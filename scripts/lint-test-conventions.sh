#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-test-conventions.sh — enforces §17 test-authoring
# conventions beyond §17.4 determinism (which has its own linter):
#
#   §17.6  Tier 1 (unit) tests use t.Parallel() by default.
#          Every TestXxx function in pkg/*_test.go should call
#          t.Parallel() unless it has a documented reason not to.
#   §17.7  No testify / gomega imports in tests/ or pkg/.
#
# §17.9 skip reasons are classified by the tier-0 Go test in
# tests/tier0_static/, which reads the syntax tree of every skip call
# site and fails the tier. One convention has one implementation, so
# nothing here reads a skip reason.
#
# Exit code:
#   0  no violations
#   N  N violations across all checks

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

violations=0
report() {
    echo "lint-test-conventions: $1" >&2
    violations=$((violations + 1))
}

# §17.7 — no testify / gomega imports in tests/ or pkg/.
while IFS= read -r match; do
    report "§17.7 forbidden import: ${match}"
done < <(grep -RIEn '"github.com/(stretchr/testify|onsi/gomega)"' "${ROOT}/pkg" "${ROOT}/tests" 2>/dev/null | grep -v testdata || true)

# §17.6 — every TestXxx in pkg/*_test.go should call t.Parallel().
# Bound the check to pkg/ (Tier 1 unit). Other tiers have explicit
# parallelism rules (component: per-test schemas; integration:
# serial within suite) so we don't gate them here.
pkg_test_files=()
while IFS= read -r f; do
    pkg_test_files+=("${f}")
done < <(find "${ROOT}/pkg" -name '*_test.go' -not -name 'fuzz_test.go' -not -name 'property_test.go' 2>/dev/null)

for f in "${pkg_test_files[@]}"; do
    # Skip files that opt out via a top-of-file marker.
    if head -10 "${f}" 2>/dev/null | grep -q 'lint-test-conventions:exempt-parallel'; then
        continue
    fi
    # Functions whose body lacks `t.Parallel()`. Use awk to walk
    # function blocks.
    awk '
        BEGIN { fn = ""; depth = 0; has_parallel = 0 }
        /^func Test[A-Z][A-Za-z0-9_]*\(/ {
            if (fn != "" && !has_parallel) {
                printf "%s:%d: %s lacks t.Parallel()\n", FILENAME, fn_line, fn
            }
            fn = $2
            sub(/\(.*$/, "", fn)
            fn_line = NR
            depth = 0
            has_parallel = 0
        }
        fn != "" {
            for (i = 1; i <= length($0); i++) {
                c = substr($0, i, 1)
                if (c == "{") depth++
                if (c == "}") {
                    depth--
                    if (depth == 0) {
                        if (!has_parallel) {
                            printf "%s:%d: %s lacks t.Parallel()\n", FILENAME, fn_line, fn
                        }
                        fn = ""
                        has_parallel = 0
                        break
                    }
                }
            }
            if (index($0, "t.Parallel()") > 0) has_parallel = 1
        }
    ' "${f}" | while IFS= read -r line; do
        report "§17.6 ${line}"
    done
done

if (( violations > 0 )); then
    echo "lint-test-conventions: ${violations} violation(s)" >&2
    exit 1
fi

echo "lint-test-conventions: no violations"
exit 0
