#!/usr/bin/env bash
#
# close-build-gaps.sh
#
# Drives `claude -p` in a loop to close every OPEN finding in
# BUILD-GAPS.md. Each iteration invokes a fresh non-interactive
# Claude session whose prompt is the standing batch-processing
# instructions; Claude picks the next batch, fixes the findings,
# commits, and updates BUILD-GAPS.md. Persistent state lives in
# git history + BUILD-GAPS.md, so the script is safely re-runnable.
#
# Stop conditions:
#   - All findings are CLOSED (success).
#   - MAX_ITER reached (default 1000).
#
# Logs land in <cwd>/tmp/ — run the script from the Lenny repo root.
# A single running log (close-build-gaps.log) collects each
# iteration's short summary, demarcated by a header line. The
# summary log (summary.log) keeps one-line per-iteration progress
# pings. The running log is passed to each next Claude session so
# the loop carries continuity even though each `claude -p` is
# stateless.
#
# Usage (from the Lenny repo root):
#   ./close-build-gaps.sh
#   ./close-build-gaps.sh --max-iter 50
#   ./close-build-gaps.sh --dry-run    # print the prompt; do not invoke
#

set -uo pipefail

REPO="$(pwd)"
LOG_DIR="$REPO/tmp"
MAX_ITER=1000
DRY_RUN=0

# Sanity-check: refuse to run outside a Lenny checkout.
if [[ ! -f "$REPO/BUILD-GAPS.md" ]]; then
  echo "error: $REPO/BUILD-GAPS.md not found — run this script from the Lenny repo root." >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --max-iter) MAX_ITER="$2"; shift 2 ;;
    --dry-run)  DRY_RUN=1; shift ;;
    -h|--help)
      sed -n '3,30p' "$0"
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

mkdir -p "$LOG_DIR"
SUMMARY="$LOG_DIR/summary.log"
RUNNING_LOG="$LOG_DIR/close-build-gaps.log"

count_open()   { grep -c '^### - \[ \] F-.*— OPEN' "$REPO/BUILD-GAPS.md" 2>/dev/null || echo 0; }
count_closed() { grep -c '— CLOSED'                "$REPO/BUILD-GAPS.md" 2>/dev/null || echo 0; }

# The standing prompt. Each `claude -p` invocation is stateless, so
# everything Claude needs to know is in here. BUILD-GAPS.md and git
# history carry the cross-iteration state.
read -r -d '' PROMPT <<'PROMPT_EOF' || true
You are continuing a long-running effort to close every OPEN finding in BUILD-GAPS.md. Each invocation handles one batch and exits.

WORKING DIRECTORY: the current working directory (the Lenny repo root). All file paths in this prompt are relative to it.

CONTINUITY: the running log of every prior iteration's short summary is at the path in `LENNY_RUNNING_LOG`. Start by reading its tail (`tail -300 "$LENNY_RUNNING_LOG"`) when it exists and is non-empty, so you know which findings were just closed, what was deferred, and what each prior batch reported. Use it to avoid re-attempting work and to extend rather than duplicate prior batches.

YOUR TASK THIS INVOCATION

1. Pick the next 4–8 OPEN findings from BUILD-GAPS.md. Prefer clustering on a shared §X.Y section or on text-flagged duplicates so one fix can close several. Start with:

       grep -n '^### - \[ \] F-.*— OPEN' BUILD-GAPS.md | head -20

   Read the full text of each candidate (the heading plus the bulleted Spec/Evidence/Gap/Suggested-resolution lines) before committing to the batch.

2. Re-verify each finding against the current code and spec. The codebase has been changing rapidly; a finding may already be addressed by a prior batch (a false alarm, or fixed in passing). If so, mark CLOSED with a one-line resolution note pointing at the commit that resolved it and move on.

3. For each genuine finding, make the spec-compliant fix:
   - Search the codebase broadly — do not restrict yourself to the file/line pointers cited in the finding text. The codebase is large and the cited paths may be stale.
   - Cite spec sections in code comments (e.g. `// spec: §X.Y line N`) and in test names.

4. Tests are mandatory:
   - Tier-1 unit tests for every change.
   - Add higher-tier tests (component tier 2, contract tier 3, integration tier 4, e2e tier 5, chaos tier 8, security tier 9) when the contract you're changing warrants them. See TESTING.md.
   - Cover happy path + edge cases (empty / error / concurrent / boundary).

5. Commit in stages — one logical group per commit. Run `go build ./...` and the relevant `go test ./pkg/...` between commits so a disconnect leaves the working tree in a buildable state.

6. After each finding is fixed, edit BUILD-GAPS.md:
   - Change `— OPEN` to `— CLOSED` on the heading.
   - Add a one or two sentence Resolution note at the bottom of the finding block, citing the commit SHA when helpful.

7. Output a TIGHT summary (≤200 words; the loop appends it to a running log, so brevity matters). Format:
   - Findings closed: bullet list of IDs with a half-line each.
   - Duplicates also closed (one bullet each).
   - Commits: SHAs only.
   - Tests added: count + tier (one line).
   - Deferred: ID + half-line reason (or "none").
   Do NOT restate the rules, the spec, file paths, or per-finding diffs in the summary.

HARD RULES

- NEVER modify any file under `spec/`. The spec is read-only by project policy (a hook will block the write).
- No backward-compatibility shims. The codebase is pre-deployment; change interfaces freely.
- If a finding text flags a "Potential duplicate" or "Potential overlap", verify the cross-reference; when you confirm overlap, close the dup in the same batch with a "Closed by F-X.Y.Z" note.
- Tread carefully — large codebase. Read enough surrounding context before each change.

WHEN DONE

Exit cleanly. A driver loop will invoke a fresh session for the next batch. The hook-tracked goal is "all findings in BUILD-GAPS.md are marked as closed"; the loop terminates when `grep -c '^### - \[ \] F-.*— OPEN' BUILD-GAPS.md` returns 0.
PROMPT_EOF

if (( DRY_RUN )); then
  echo "=== DRY RUN — prompt that would be sent ==="
  printf '%s\n' "$PROMPT"
  echo "==========================================="
  exit 0
fi

# run_claude_capture invokes `claude -p --output-format json`,
# captures both the session_id (for resume on failure) and the
# textual result. Outputs land in the script-scope globals
# CLAUDE_SESSION, CLAUDE_RESULT, CLAUDE_RC so the caller can branch
# on rc and resume by id.
CLAUDE_SESSION=""
CLAUDE_RESULT=""
CLAUDE_RC=0

run_claude_capture() {
  local prompt="$1"
  local resume_id="$2"
  local raw
  raw=$(mktemp)
  local cmd=(claude -p
        --permission-mode bypassPermissions
        --add-dir "$REPO"
        --output-format json)
  if [[ -n "$resume_id" ]]; then
    cmd+=(--resume "$resume_id")
  fi
  cmd+=("$prompt")

  LENNY_RUNNING_LOG="$RUNNING_LOG" "${cmd[@]}" >"$raw" 2>&1
  CLAUDE_RC=$?

  # Parse the JSON envelope (success path) or pass the raw text
  # through (on parse failure — usually a CLI-level error).
  CLAUDE_SESSION=$(python3 - "$raw" <<'PY' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    print(d.get("session_id", ""))
except Exception:
    pass
PY
)
  CLAUDE_RESULT=$(python3 - "$raw" <<'PY' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    print(d.get("result") or d.get("error") or "")
except Exception:
    with open(sys.argv[1]) as f:
        print(f.read())
PY
)
  if [[ -z "$CLAUDE_RESULT" ]]; then
    CLAUDE_RESULT=$(cat "$raw")
  fi
  rm -f "$raw"
}

# run_iter executes one batch, appends Claude's short summary to
# the running log, and emits exactly one progress line to the
# summary log. On non-zero exit it retries up to MAX_RETRIES by
# resuming the same session.
# Returns 0 if there is more work to do, 2 if all findings are CLOSED.
MAX_RETRIES=3

run_iter() {
  local iter="$1"
  local before_closed before_open after_closed after_open delta
  before_closed=$(count_closed)
  before_open=$(count_open)

  if (( before_open == 0 )); then
    printf '[%s] iter=%d open=0 — all findings CLOSED\n' \
      "$(date -Iseconds)" "$iter" | tee -a "$SUMMARY"
    return 2
  fi

  cd "$REPO" || { echo "cd $REPO failed"; return 0; }

  printf '\n========== iter=%d  %s  closed=%s  open=%s ==========\n' \
    "$iter" "$(date -Iseconds)" "$before_closed" "$before_open" \
    >>"$RUNNING_LOG"

  # First attempt: fresh session.
  run_claude_capture "$PROMPT" ""
  printf '%s\n' "$CLAUDE_RESULT" >>"$RUNNING_LOG"

  # Retries: resume the captured session id when we have one;
  # otherwise restart fresh.
  local retries=0
  local resume_prompt='Resume the prior batch. Re-read BUILD-GAPS.md to see the current state, finish any in-flight work cleanly (commit + mark CLOSED + brief summary), and exit. Keep the summary ≤200 words.'
  while (( CLAUDE_RC != 0 )) && (( retries < MAX_RETRIES )); do
    retries=$(( retries + 1 ))
    if [[ -n "$CLAUDE_SESSION" ]]; then
      printf '[retry %d/%d resuming session=%s]\n' \
        "$retries" "$MAX_RETRIES" "$CLAUDE_SESSION" >>"$RUNNING_LOG"
      run_claude_capture "$resume_prompt" "$CLAUDE_SESSION"
    else
      printf '[retry %d/%d no session captured — fresh start]\n' \
        "$retries" "$MAX_RETRIES" >>"$RUNNING_LOG"
      run_claude_capture "$PROMPT" ""
    fi
    printf '%s\n' "$CLAUDE_RESULT" >>"$RUNNING_LOG"
  done

  after_closed=$(count_closed)
  after_open=$(count_open)
  delta=$(( after_closed - before_closed ))

  printf '[%s] iter=%d delta=+%d closed=%s open=%s rc=%d retries=%d\n' \
    "$(date -Iseconds)" "$iter" "$delta" "$after_closed" "$after_open" \
    "$CLAUDE_RC" "$retries" \
    | tee -a "$SUMMARY"

  return 0
}

iter=0
while (( iter < MAX_ITER )); do
  iter=$(( iter + 1 ))
  run_iter "$iter"
  rc=$?
  if (( rc == 2 )); then
    break
  fi
done

final_closed=$(count_closed)
final_open=$(count_open)
printf '[%s] FINAL iter=%d closed=%s open=%s\n' \
  "$(date -Iseconds)" "$iter" "$final_closed" "$final_open" \
  | tee -a "$SUMMARY"

if (( final_open == 0 )); then
  exit 0
fi
exit 1
