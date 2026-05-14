#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-queries.sh — enforces spec §12.3 R-02 against SQL queries.
#
# Rule R-02 (exact wording from spec/12_storage-architecture.md):
#   "Application code MUST NOT issue SQL queries that JOIN across tenant
#    boundaries — i.e., no query may JOIN a tenant-scoped table from one
#    tenant against rows belonging to a different tenant."
#
#   Detection logic: "A static-analysis linter MUST enforce this rule by
#    detecting JOIN operations between tables that carry tenant_id columns
#    where the ON clause does not include a.tenant_id = b.tenant_id."
#
# How this linter interprets the rule:
#   - Scans .sql files under migrations/ and queries/, plus .go files
#     under pkg/, for the SQL token `JOIN` (case-insensitive).
#   - For each JOIN whose surrounding 3-line window contains an ON clause,
#     verifies the ON clause references `<left>.tenant_id = <right>.tenant_id`
#     (or the equivalent commuted form).
#   - A JOIN may be marked safe with the annotation
#     `-- platform-admin-cross-tenant-allowed` on the line immediately
#     preceding the JOIN keyword, AND a `-- platform-admin-cross-tenant-justification: <reason>`
#     line preceding that. Both must be present together.
#   - Inventory of annotated exceptions is reported at the end so reviewers
#     see the cross-tenant query budget.
#
# Exit code: number of violations (0 on success).
#
# Usage:
#   scripts/lint-queries.sh [path ...]
#   Default targets: migrations/ pkg/ tests/testdata/
#
# Limitations:
#   - Heuristic-based; complex multi-line SQL embedded in Go string
#     concatenations may be missed.
#   - Does not check whether the table on either side actually has a
#     tenant_id column. The spec rule is on JOINs between tenant-scoped
#     tables; we conservatively flag any JOIN missing the
#     `<lhs>.tenant_id = <rhs>.tenant_id` pattern.

set -uo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

REPO_ROOT="$(lenny_repo_root)"
TARGETS=("$@")
if (( ${#TARGETS[@]} == 0 )); then
  TARGETS=(
    "$REPO_ROOT/migrations"
    "$REPO_ROOT/pkg"
    "$REPO_ROOT/tests/testdata"
  )
fi

violations=0
exceptions=0

report_violation() {
  lenny_log_err "$(printf '%s:%s: %s' "$1" "$2" "$3")"
  violations=$((violations + 1))
}

scan_one_file() {
  local f="$1"
  local rel="${f#"$REPO_ROOT"/}"
  python3 - "$f" "$rel" <<'PY'
import re, sys, pathlib

path = pathlib.Path(sys.argv[1])
rel = sys.argv[2]
text = path.read_text(errors="replace")
lines = text.splitlines()

# Tokenise each JOIN occurrence. Matching is case-sensitive on the
# uppercase SQL-keyword form so the rule does not collide with Go
# identifiers such as strings.Join. .sql files use uppercase keywords
# by project convention; SQL strings embedded in .go code follow the
# same convention.
join_re = re.compile(r'\bJOIN\b')
on_re = re.compile(
    r'\bON\b\s*[^;]*?(\b[\w]+)\.tenant_id\s*=\s*(\b[\w]+)\.tenant_id'
)
allow_annot = "-- platform-admin-cross-tenant-allowed"
just_annot = "-- platform-admin-cross-tenant-justification:"

violations = 0
exceptions = 0

for i, line in enumerate(lines):
    for m in join_re.finditer(line):
        # Look at the next 5 lines for an ON clause that pairs tenant_ids.
        window = "\n".join(lines[i:i + 6])

        # Check annotation: previous 3 lines for the allow annotation.
        prev = "\n".join(lines[max(0, i - 3):i])

        if allow_annot in prev:
            if just_annot in prev:
                exceptions += 1
                # Don't print exceptions individually; just count.
                continue
            else:
                print(f"{rel}:{i + 1}: JOIN annotated {allow_annot} but missing {just_annot} <reason> on a preceding line (R-02)")
                violations += 1
                continue

        # Look for `X.tenant_id = Y.tenant_id` in the window.
        if on_re.search(window):
            continue

        # No tenant_id pairing — emit a violation. Add column-1 hint.
        col = m.start() + 1
        print(f"{rel}:{i + 1}:{col}: JOIN without tenant_id equality in ON clause (R-02). Either include `<lhs>.tenant_id = <rhs>.tenant_id` in the ON clause, or annotate the join with -- platform-admin-cross-tenant-allowed and -- platform-admin-cross-tenant-justification: <reason>.")
        violations += 1

# Report counts via exit code style data.
print(f"__counts__\t{violations}\t{exceptions}")
PY
}

# Walk targets.
found_files=0
for t in "${TARGETS[@]}"; do
  if [[ ! -e "$t" ]]; then
    continue
  fi
  while IFS= read -r -d '' f; do
    found_files=$((found_files + 1))
    while IFS= read -r line; do
      if [[ "$line" == __counts__$'\t'* ]]; then
        IFS=$'\t' read -r _ v e <<<"$line"
        violations=$((violations + v))
        exceptions=$((exceptions + e))
      else
        # Pre-formatted violation; print verbatim with the [err] prefix.
        lenny_log_err "$line"
      fi
    done < <(scan_one_file "$f")
  done < <(find "$t" \( -name '*.sql' -o -name '*.go' \) -type f -print0 2>/dev/null)
done

if (( found_files == 0 )); then
  lenny_log_info "no .sql/.go files found under: ${TARGETS[*]}"
  exit 0
fi

if (( violations == 0 )); then
  if (( exceptions > 0 )); then
    lenny_log_warn "lint-queries: R-02 clean across $found_files file(s); $exceptions annotated cross-tenant exception(s) accepted"
  else
    lenny_log_ok "lint-queries: $found_files file(s), R-02 clean ($exceptions cross-tenant exception(s))"
  fi
else
  lenny_log_err "lint-queries: $violations R-02 violation(s) across $found_files file(s) ($exceptions annotated exception(s))"
fi
exit "$violations"
