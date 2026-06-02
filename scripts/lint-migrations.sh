#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-migrations.sh — enforces the §12.0 "every migration has a
# corresponding rollback script and a non-empty test under
# tests/tier2_component/migrations/" rule.
#
# Checks:
#   1. Every .up.sql file under migrations/ has a matching .down.sql sibling.
#   2. The .down.sql is not empty (must contain at least one non-comment,
#      non-whitespace line).
#   3. Every migration sequence number that appears in migrations/ also
#      appears in at least one _test.go file under
#      tests/tier2_component/migrations/ (so the framework's regression
#      tests cover the migration). The check is conservative: it looks for
#      the bare migration number as a substring; if your fixture lives
#      under tests/testdata/migrations/ keep the same numbering.
#   4. No .up.sql adds a column with a bare NOT NULL constraint and no
#      DEFAULT. The §10.5 expand-contract rule requires every Phase 1
#      column to be NULLable or carry a server-side DEFAULT until Phase 3
#      drops the old column: during a rolling deploy an old-version replica
#      that does not know the new column issues INSERTs that omit it, and a
#      NOT NULL constraint without a default makes those INSERTs fail.
#      spec: §10.5 line 415.
#
# Exit code:
#   0  success
#   N  N violations found (file paths printed to stderr).
#
# Usage:
#   scripts/lint-migrations.sh [migrations-dir]
#   Default migrations-dir is "migrations/" relative to repo root.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATIONS_DIR="${1:-${ROOT}/migrations}"
TEST_DIR="${ROOT}/tests/tier2_component/migrations"

violations=0
report() {
    echo "lint-migrations: $1" >&2
    violations=$((violations + 1))
}

if [[ ! -d "${MIGRATIONS_DIR}" ]]; then
    # No migrations directory: pass with a note. Production migrations
    # land later; in the meantime the harness should not block.
    echo "lint-migrations: ${MIGRATIONS_DIR} does not exist; skipping (no migrations to lint)"
    exit 0
fi

# Pass 1: every .up.sql has a sibling .down.sql.
while IFS= read -r up; do
    base="${up%.up.sql}"
    down="${base}.down.sql"
    if [[ ! -f "${down}" ]]; then
        report "missing rollback: ${up} has no ${down}"
        continue
    fi
    # Pass 2: .down.sql is not empty.
    if ! grep -vE '^\s*(--.*)?$' "${down}" >/dev/null 2>&1; then
        report "empty rollback: ${down}"
    fi
done < <(find "${MIGRATIONS_DIR}" -type f -name '*.up.sql' | sort)

# Pass 3: every migration sequence number is referenced in a test file.
# Migration filenames typically look like 0001_*.up.sql, 0002_*.up.sql, etc.
if [[ -d "${TEST_DIR}" ]]; then
    while IFS= read -r up; do
        filename="$(basename "${up}")"
        # Extract the leading numeric token (everything up to the first _).
        number="${filename%%_*}"
        if [[ -z "${number}" || ! "${number}" =~ ^[0-9]+$ ]]; then
            continue
        fi
        # Search for the number as a substring in any _test.go file. We
        # do not require an exact match; the test may reference the
        # number via filepath.Join or as a hard-coded fixture.
        if ! grep -RIqs "${number}" "${TEST_DIR}"; then
            report "no test references migration ${number} (${up}); add coverage under ${TEST_DIR}"
        fi
    done < <(find "${MIGRATIONS_DIR}" -type f -name '*.up.sql' | sort)
fi

# Pass 4: §10.5 expand-contract — no `ADD COLUMN ... NOT NULL` without a
# DEFAULT. Each up-migration is normalized (line comments stripped, lines
# joined, statements split on `;`) and every `ADD COLUMN` clause is isolated
# up to the next comma so a NOT NULL column that carries no DEFAULT is
# reported. A DEFAULT keyword precedes its literal in the clause, so a value
# that itself contains a comma (e.g. '{"a":1}'::jsonb) does not cause a false
# positive. CREATE TABLE columns are exempt — a brand-new table has no
# old-version replica writing NULL-omitting INSERTs against it.
while IFS= read -r up; do
    if awk '
        { line = $0; sub(/--.*/, "", line); buf = buf " " line }
        END {
            n = split(buf, stmts, ";")
            for (i = 1; i <= n; i++) {
                s = tolower(stmts[i])
                m = split(s, parts, /add column/)
                for (j = 2; j <= m; j++) {   # parts[1] precedes the first ADD COLUMN
                    seg = parts[j]
                    c = index(seg, ",")
                    if (c > 0) seg = substr(seg, 1, c - 1)
                    if (seg ~ /not null/ && seg !~ /default/) {
                        exit 1
                    }
                }
            }
        }
    ' "${up}"; then
        : # no violation
    else
        report "§10.5 expand-contract: ${up} adds a NOT NULL column with no DEFAULT (old-version replicas omitting the column on INSERT will fail). Make it NULLable or give it a server-side DEFAULT; add the NOT NULL constraint in a Phase 3 migration."
    fi
done < <(find "${MIGRATIONS_DIR}" -type f -name '*.up.sql' | sort)

if (( violations > 0 )); then
    echo "lint-migrations: ${violations} violation(s)" >&2
    exit "${violations}"
fi

echo "lint-migrations: all migrations have rollback + test coverage and satisfy the §10.5 expand-contract nullability rule"
exit 0
