#!/usr/bin/env bash
# Layer 4: the build loop's own prompt, checked against the surfaces it names.
#
# close-build-gaps.sh hands an agent a prompt naming a skill modifier and a
# proposal status. Both drifted from the things they name and nothing caught
# it: the loop asked for an `apply-only` modifier the skill never declared, so
# the run silently became a full one, and it short-circuited on a status
# ("Applied to spec") that the four-state model cannot hold, so the guard never
# fired. These assertions are cross-file consistency checks rather than greps
# for a fixed phrase, so they keep holding if either side is renamed again.
#
# Run: bash .claude/tests/build-loop.test.sh

set -uo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SH="$REPO/close-build-gaps.sh"
SKILL="$REPO/.claude/skills/implement-proposal/SKILL.md"
STATUS_TOOL="$REPO/.claude/tools/proposal-status.mjs"
fails=0; checks=0
check() { checks=$((checks+1)); if [ "$2" = "$3" ]; then echo "  PASS  $1"; else echo "  FAIL  $1  :: expected [$2] got [$3]"; fails=$((fails+1)); fi; }
# The haystack here is a whole multi-page agent prompt, so a failure prints the
# needle and a clipped haystack rather than the several thousand lines that
# would bury every other line of the run's output.
clip() { printf '%s' "$1" | tr '\n' ' ' | cut -c1-160; }
contains() { checks=$((checks+1)); if printf '%s' "$2" | grep -qF -- "$3"; then echo "  PASS  $1"; else echo "  FAIL  $1  :: lacks [$3]  in [$(clip "$2")...]"; fails=$((fails+1)); fi; }
lacks() { checks=$((checks+1)); if printf '%s' "$2" | grep -qF -- "$3"; then echo "  FAIL  $1  :: still carries [$3]  in [$(clip "$2")...]"; fails=$((fails+1)); else echo "  PASS  $1"; fi; }

# The lookbehind drops a flag such as `--proposals-only`, which is the script's
# own option rather than a modifier it passes to the skill.
mods_in() { grep -oP '(?<![-a-zA-Z])[a-z][a-z-]*-only' "$@" | sort -u; }
# A status the prompt names as a proposal state: a capitalised quoted phrase
# within a few words of the word Status.
statuses_in() { grep -o 'Status[^.]\{0,40\}"[A-Z][^"]*"' "$@" | grep -o '"[^"]*"' | tr -d '"' | sort -u; }

HINT="$(grep '^argument-hint:' "$SKILL")"
STATES="$(sed -n 's/^const STATES = \[\(.*\)\];/\1/p' "$STATUS_TOOL")"

echo; echo "### layer 4: close-build-gaps.sh"

echo; echo "B1. every skill modifier the loop names is one the skill declares"
check "the skill declares an argument-hint" "yes" "$([ -n "$HINT" ] && echo yes || echo no)"
for m in $(mods_in "$SH"); do
  contains "modifier $m is in argument-hint" "$HINT" "$m"
done

echo; echo "B2. every proposal status the loop names is a state the status tool can produce"
check "the status tool declares STATES" "yes" "$([ -n "$STATES" ] && echo yes || echo no)"
for s in $(statuses_in "$SH" | tr ' ' '\037'); do
  s="$(printf '%s' "$s" | tr '\037' ' ')"
  contains "status \"$s\" is a declared state" "$STATES" "\"$s\""
done

echo; echo "B3. the prompt an agent actually receives carries the same two properties"
OUT="$(cd "$REPO" && bash "$SH" --mode proposals --dry-run 2>&1)"; rc=$?
check "dry run exits 0" 0 "$rc"
PROMPT_F="$(mktemp)"; printf '%s\n' "$OUT" > "$PROMPT_F"
for m in $(mods_in "$PROMPT_F"); do
  contains "emitted modifier $m is in argument-hint" "$HINT" "$m"
done
for s in $(statuses_in "$PROMPT_F" | tr ' ' '\037'); do
  s="$(printf '%s' "$s" | tr '\037' ' ')"
  contains "emitted status \"$s\" is a declared state" "$STATES" "\"$s\""
done
R2="$(grep -m1 '^R2\.' "$PROMPT_F")"
contains "R2 conditions re-entry on the implementation checklist" "$R2" "implementation checklist"
lacks "R2 does not condition re-entry on a status read" "$R2" "Status already reads"

HELP="$(cd "$REPO" && bash "$SH" --help 2>&1)"
HELP_F="$(mktemp)"; printf '%s\n' "$HELP" > "$HELP_F"
for m in $(mods_in "$HELP_F"); do
  contains "--help modifier $m is in argument-hint" "$HINT" "$m"
done
for s in $(statuses_in "$HELP_F" | tr ' ' '\037'); do
  s="$(printf '%s' "$s" | tr '\037' ' ')"
  contains "--help status \"$s\" is a declared state" "$STATES" "\"$s\""
done
rm -f "$PROMPT_F" "$HELP_F"

echo; echo "B4. the prompt states who sets the terminal status"
contains "names the CLI that sets Implemented" "$OUT" "--set status=Implemented"
contains "names it as a human action" "$OUT" "human"

echo
if [ "$fails" -eq 0 ]; then echo "build-loop.test.sh: all green ($checks checks)"; exit 0; fi
echo "build-loop.test.sh: $fails/$checks checks FAILED"; exit 1
