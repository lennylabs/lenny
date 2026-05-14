#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-determinism.sh — enforces §17.4 determinism rules
# across the test surface:
#
#   1. No `time.Now()` in *_test.go (use testinfra/timectl)
#   2. No naked `time.Sleep` (use testinfra/wait.For)
#   3. No `math/rand` or `crypto/rand` in *_test.go (use testinfra/randctl)
#   4. No port literals in net.Listen / Dial (use testinfra/ports)
#   5. No fmt.Println in *_test.go (use t.Log)
#
# Exemptions:
#   - tests/testinfra/{timectl,randctl,wait,ports,goleak,golden}/ —
#     these implement the helpers themselves
#   - cmd/lenny-test/ — harness code, not tests
#   - Files with a `// lint-determinism:exempt` marker on the first
#     20 lines (escape hatch for vendored or generated tests)
#
# Exit code:
#   0  no violations (or no test files to scan)
#   N  N violations across all checks (counts file × rule)

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

violations=0
report() {
    echo "lint-determinism: $1" >&2
    violations=$((violations + 1))
}

# Build the list of *_test.go files to scan. Exclude testinfra
# helpers that implement the determinism primitives themselves.
files=()
while IFS= read -r f; do
    case "${f}" in
        */tests/testinfra/timectl/*|*/tests/testinfra/randctl/*|*/tests/testinfra/wait/*|*/tests/testinfra/ports/*|*/tests/testinfra/goleak/*|*/tests/testinfra/golden/*|*/tests/testinfra/load/*|*/tests/testinfra/compose/*|*/tests/testinfra/kind/*|*/tests/testinfra/containers/*)
            continue
            ;;
    esac
    # Honor the per-file exemption marker.
    if head -20 "${f}" 2>/dev/null | grep -q "lint-determinism:exempt"; then
        continue
    fi
    files+=("${f}")
done < <(find "${ROOT}/tests" "${ROOT}/pkg" -name '*_test.go' -not -path '*/testdata/*' -not -path '*/.git/*' 2>/dev/null)

if [[ ${#files[@]} -eq 0 ]]; then
    echo "lint-determinism: no test files to scan"
    exit 0
fi

check_pattern() {
    local rule="$1" pattern="$2"
    while IFS= read -r match; do
        # match format: file:line:content
        report "${rule}: ${match}"
    done < <(grep -EHn "${pattern}" "${files[@]}" 2>/dev/null || true)
}

# Rule 1: time.Now() in tests. Allow time.Time literals and zero values.
check_pattern "time.Now() in test" '\btime\.Now\(\)'

# Rule 2: naked time.Sleep. The check is conservative — even a
# t.Helper-marked helper that uses Sleep would flag.
check_pattern "time.Sleep in test (use testinfra/wait.For)" '\btime\.Sleep\('

# Rule 3: math/rand or crypto/rand direct use.
check_pattern 'math/rand import in test' '"math/rand"'
check_pattern 'crypto/rand import in test (use testinfra/randctl.Crypto)' '"crypto/rand"'

# Rule 4: net.Listen / net.Dial with a hard-coded port.
check_pattern 'net.Listen with port literal' 'net\.Listen\([^,]+,\s*"[^"]*:[0-9]+"'
check_pattern 'net.Dial with port literal' 'net\.Dial(Timeout)?\([^,]+,\s*"[^"]*:[0-9]+"'

# Rule 5: fmt.Println in tests (use t.Log).
check_pattern 'fmt.Println in test (use t.Log)' '\bfmt\.Println\('

if (( violations > 0 )); then
    echo "lint-determinism: ${violations} violation(s)" >&2
    exit 1
fi

echo "lint-determinism: ${#files[@]} test file(s) scanned; no violations"
exit 0
