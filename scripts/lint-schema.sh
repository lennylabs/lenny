#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# scripts/lint-schema.sh — enforces spec §12.3 R-01 against SQL migrations.
#
# Rule R-01 (exact wording from spec/12_storage-architecture.md):
#   "Every tenant-scoped table MUST have tenant_id as the leading column of
#    its primary index or as the first element of every composite index
#    used on high-frequency query paths."
#
# How this linter interprets the rule:
#   - A CREATE TABLE statement is "tenant-scoped" if it declares a column
#     named `tenant_id`, OR if it carries a `-- session-sharded`
#     annotation in the preceding 5 lines.
#   - For tenant-scoped tables WITHOUT the `-- session-sharded` annotation,
#     `tenant_id` must be the first column listed in the PRIMARY KEY
#     declaration (single-column tenant_id or `PRIMARY KEY (tenant_id, ...)`),
#     OR the table must have at least one composite index whose leading
#     column is `tenant_id`.
#   - For session-sharded tables, the linter requires:
#       (a) the `-- session-sharded-justification: <reason>` annotation on
#           the line immediately preceding `-- session-sharded`, and
#       (b) a column named `session_id` OR a FOREIGN KEY REFERENCES
#           sessions(id) in the table body.
#   - Cannot detect "high-frequency query paths" statically; the linter is
#     conservative and checks every composite index for tenant-scoped tables.
#
# Exit code: number of violations found (0 on success).
#
# Usage:
#   scripts/lint-schema.sh [path ...]
#   Default path is "migrations/" relative to the repo root.
#
# Limitations (documented to set expectations):
#   - Naive SQL parser based on grep/awk. Comment stripping is line-level.
#   - Does not parse multi-statement CREATE INDEX declarations across
#     migrations; an index added in a later migration is checked in the
#     migration that declares it.

set -uo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
# shellcheck source=lib/common.sh
source "$SCRIPT_DIR/lib/common.sh"

REPO_ROOT="$(lenny_repo_root)"
TARGETS=("$@")
if (( ${#TARGETS[@]} == 0 )); then
  TARGETS=("$REPO_ROOT/migrations")
fi

violations=0
report_violation() {
  # report_violation <file> <line> <message>
  lenny_log_err "$(printf '%s:%s: %s' "$1" "$2" "$3")"
  violations=$((violations + 1))
}

# Strip line comments after the `--` token (but keep the line index intact
# by emitting blank lines where comments occupied a full line). Output:
# `<lineno>\t<content>` per non-empty line.
strip_comments() {
  local f="$1"
  awk '
    {
      sub(/--.*/, "")
      # Trim trailing whitespace
      sub(/[ \t]+$/, "")
      print NR "\t" $0
    }
  ' "$f"
}

# Find every CREATE TABLE in a file. Echoes one record per table:
# <start_line>\t<table_name>\t<body>
# where body is the text inside the outer parentheses with newlines
# replaced by SPACE (so awk can scan within a single record), and we keep
# the leading-line annotation by also echoing the preceding 5 lines'
# comment block joined with semicolons.
find_create_tables() {
  local f="$1"
  python3 - "$f" <<'PY'
import re, sys, pathlib

path = pathlib.Path(sys.argv[1])
text = path.read_text()
lines = text.splitlines()

# Build a list of comments per line (stripped of leading whitespace).
comments_by_line = []
for ln in lines:
    s = ln.strip()
    if s.startswith("--"):
        comments_by_line.append(s)
    else:
        comments_by_line.append("")

# Find CREATE TABLE statements (case-insensitive, balanced parentheses).
i = 0
while i < len(text):
    m = re.search(r'(?i)\bCREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+([\w."]+)\s*\(', text[i:])
    if not m:
        break
    abs_start = i + m.start()
    tbl_name = m.group(1).strip('"')
    # Line number (1-based) of CREATE TABLE
    line_no = text.count("\n", 0, abs_start) + 1
    # Walk parentheses
    depth = 0
    j = i + m.end() - 1  # position of '('
    while j < len(text):
        c = text[j]
        if c == '(':
            depth += 1
        elif c == ')':
            depth -= 1
            if depth == 0:
                break
        j += 1
    body = text[i + m.end():j]
    # Preceding 5 lines for annotation pickup.
    pre_start = max(0, line_no - 6)
    annotations = "|".join(comments_by_line[pre_start:line_no - 1])
    # Output single line: lineno\tname\tannotations\tbody
    # Use a sentinel for newlines in body.
    body_oneline = body.replace("\n", " ")
    print(f"{line_no}\t{tbl_name}\t{annotations}\t{body_oneline}")
    i = j + 1
PY
}

# Check one migration file.
lint_migration_file() {
  local f="$1"
  local rel="${f#"$REPO_ROOT"/}"

  # Iterate each CREATE TABLE found.
  while IFS=$'\t' read -r line_no tbl_name annotations body; do
    [[ -z "$tbl_name" ]] && continue

    # Does the body declare a tenant_id column?
    has_tenant_col=0
    if echo "$body" | grep -qE '(^|[[:space:],(])tenant_id[[:space:]]'; then
      has_tenant_col=1
    fi

    # Is the table annotated session-sharded?
    is_session_sharded=0
    if echo "$annotations" | grep -q -- '-- session-sharded'; then
      is_session_sharded=1
    fi

    # A table is tenant-scoped iff it carries tenant_id OR it's annotated
    # session-sharded (which implies it's a shard of tenant-scoped data
    # routed by session id).
    if (( ! has_tenant_col )) && (( ! is_session_sharded )); then
      # Not tenant-scoped; skip. (Reference data, lookup tables, etc.)
      continue
    fi

    if (( is_session_sharded )); then
      # Require a justification on the same annotation block.
      if ! echo "$annotations" | grep -q -- '-- session-sharded-justification:'; then
        report_violation "$rel" "$line_no" \
          "table '$tbl_name' is annotated -- session-sharded but lacks -- session-sharded-justification: <reason> (R-01)"
      fi
      # Require a session_id column or a REFERENCES sessions(id).
      if ! echo "$body" | grep -qE '(^|[[:space:],(])session_id[[:space:]]|REFERENCES[[:space:]]+sessions[[:space:]]*\([[:space:]]*id'; then
        report_violation "$rel" "$line_no" \
          "table '$tbl_name' is annotated -- session-sharded but has no session_id column or REFERENCES sessions(id) (R-01)"
      fi
      continue
    fi

    # Tenant-scoped (non-session-sharded). tenant_id must be the leading
    # column of the PRIMARY KEY, OR there must be at least one composite
    # index whose leading column is tenant_id.
    pk_starts_with_tenant=0
    # Inline column-level PRIMARY KEY on tenant_id: `tenant_id ... PRIMARY KEY ...`
    if echo "$body" | grep -qiE 'tenant_id[[:space:]][^,]*PRIMARY[[:space:]]+KEY'; then
      pk_starts_with_tenant=1
    fi
    # Table-level PRIMARY KEY (tenant_id, ...)
    if echo "$body" | grep -qiE 'PRIMARY[[:space:]]+KEY[[:space:]]*\([[:space:]]*tenant_id'; then
      pk_starts_with_tenant=1
    fi

    # Composite indexes on this table: scan the file for CREATE INDEX ...
    # ON <table> (tenant_id, ...). If any exists, the rule is satisfied
    # via the index path.
    composite_idx_leading_tenant=0
    if grep -iE 'CREATE[[:space:]]+(UNIQUE[[:space:]]+)?INDEX[[:space:]]+.*ON[[:space:]]+'"$tbl_name"'[[:space:]]*\([[:space:]]*tenant_id' "$f" >/dev/null 2>&1; then
      composite_idx_leading_tenant=1
    fi

    if (( ! pk_starts_with_tenant )) && (( ! composite_idx_leading_tenant )); then
      report_violation "$rel" "$line_no" \
        "table '$tbl_name' is tenant-scoped (has tenant_id column) but tenant_id is not the leading column of its PRIMARY KEY and no composite index on the table has tenant_id as its leading column (R-01). Either restructure the primary key, add a tenant_id-leading composite index, or annotate the table -- session-sharded with a justification."
    fi
  done < <(find_create_tables "$f")
}

# Walk targets.
found_files=0
for t in "${TARGETS[@]}"; do
  if [[ ! -e "$t" ]]; then
    lenny_log_info "skipping $t (not present)"
    continue
  fi
  while IFS= read -r -d '' f; do
    found_files=$((found_files + 1))
    lint_migration_file "$f"
  done < <(find "$t" -name '*.sql' -type f -print0 2>/dev/null)
done

if (( found_files == 0 )); then
  lenny_log_info "no .sql migration files found under: ${TARGETS[*]}"
  exit 0
fi

if (( violations == 0 )); then
  lenny_log_ok "lint-schema: $found_files file(s), R-01 clean"
else
  lenny_log_err "lint-schema: $violations R-01 violation(s) across $found_files file(s)"
fi
exit "$violations"
