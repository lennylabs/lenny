#!/usr/bin/env bash
# Layer 4: the per-round bookkeeping script, executed for real.
#
# This is the script an agent invokes with one command per round, and it does
# the work whose silent failure would be worst: a shard that never reaches the
# log, an audit that never fires, a snapshot that is not taken. It is tested by
# running it rather than by asserting a prompt.
#
# Run: bash .claude/tests/round-boundary.test.sh

set -uo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SH="$REPO/.claude/tools/cp-round-boundary.sh"
fails=0; checks=0
check() { checks=$((checks+1)); if [ "$2" = "$3" ]; then echo "  PASS  $1"; else echo "  FAIL  $1  :: expected [$2] got [$3]"; fails=$((fails+1)); fi; }
# -F: the expected strings are JSON fragments containing [ and ], which a
# regex would read as character classes.
contains() { checks=$((checks+1)); if printf '%s' "$2" | grep -qF -- "$3"; then echo "  PASS  $1"; else echo "  FAIL  $1  :: [$2] lacks [$3]"; fails=$((fails+1)); fi; }

TAG="rbtest$$"
T="$(mktemp -d)"; DIR="$T/proposals/0099_fix_t"
trap 'rm -rf "$T" "$REPO/scratchpad/cp-log/$TAG" "$REPO/scratchpad/cp-state/$TAG" "$REPO/scratchpad/cp-snap/$TAG" "$REPO/scratchpad/cp-args/$TAG.json"' EXIT
mkdir -p "$DIR" "$REPO/scratchpad/cp-log/$TAG" "$REPO/scratchpad/cp-args"
LOG="$DIR/0099_fix_t.review-log.md"
fresh_log() { printf '# Review log\n\n## Standing context\nnone\n\n## Ledger\n\n## Retired\nold\n' > "$LOG"; }
fresh_log
printf '# Spec changes\nstaged\n' > "$DIR/0099_fix_t.spec-changes.md"
run() { bash "$SH" --dir "$DIR" --tag "$TAG" --loop "$1" --round "$2" --repo "$REPO" "${@:3}"; }

echo; echo "### layer 4: cp-round-boundary.sh"
echo; echo "T10. shards merge under Ledger, are deleted, and the merge is idempotent"
printf -- '### [spec.1.a.1]\n- FACT: one\n' > "$REPO/scratchpad/cp-log/$TAG/spec.1.a.md"
printf -- '### [spec.1.b.1]\n- FACT: two\n' > "$REPO/scratchpad/cp-log/$TAG/spec.1.b.md"
OUT="$(run spec 1)"; rc=$?
check "exit 0" 0 "$rc"
contains "reports two merged" "$OUT" '"merged":2'
check "entries land under Ledger, before Retired" "yes" "$(awk '/^## Ledger/{f=1;next}/^## Retired/{f=0}f' "$LOG" | grep -q 'FACT: one' && echo yes || echo no)"
check "the shards are deleted" 0 "$(find "$REPO/scratchpad/cp-log/$TAG" -name 'spec.1.*' | wc -l)"
OUT2="$(run spec 1)"
contains "a second call merges nothing" "$OUT2" '"merged":0'
check "and does not duplicate the entries" 1 "$(grep -c 'FACT: one' "$LOG")"

echo; echo "T10b. a crash mid-merge leaves the unmerged shard to be picked up"
fresh_log
printf -- '### [spec.2.a.1]\n- FACT: survivor\n' > "$REPO/scratchpad/cp-log/$TAG/spec.2.a.md"
run spec 2 >/dev/null
check "the survivor is merged on the next call" 1 "$(grep -c 'FACT: survivor' "$LOG")"

echo; echo "T11. the write audit names a file changed outside the call"
run spec 3 >/dev/null                        # records the baseline hashes
printf 'edited by something\n' >> "$DIR/0099_fix_t.spec-changes.md"
OUT="$(run spec 4)"
contains "the changed file is named" "$OUT" '0099_fix_t.spec-changes.md'
OUT="$(run spec 5)"
contains "an unchanged tree reports nothing" "$OUT" '"changedFiles":[]'

echo; echo "T11b. compaction fires on the STANDING CONTEXT, which is the only section agents read"
OUT="$(run spec 6 --compact-at 100000 --standing-at 100000)"
contains "not due below both thresholds" "$OUT" '"compactionDue":false'
contains "standing lines are reported" "$OUT" '"standingLines":'
OUT="$(run spec 7 --compact-at 100000 --standing-at 1)"
contains "due on standing-context size" "$OUT" '"compactionDue":true'
# A long ledger alone does NOT trigger: nothing but the compactor reads it, so
# firing an expensive pass on its length protects against a cost that does not
# exist. It keeps a backstop bound, far higher.
fresh_log
for i in $(seq 1 60); do printf -- "- FACT: line %s\n" "$i" >> "$REPO/scratchpad/cp-log/$TAG/spec.8.g.md"; done
OUT="$(run spec 8 --compact-at 100000 --standing-at 100000)"
contains "a long ledger alone does not trigger it" "$OUT" '"compactionDue":false'
OUT="$(run spec 9 --compact-at 1 --standing-at 100000)"
contains "but the ledger backstop still can" "$OUT" '"compactionDue":true'

echo; echo "T11c. the snapshot for the next round is taken, and hunks are counted"
OUT="$(run spec 9)"
contains "the next round's snapshot path is reported" "$OUT" "cp-snap/$TAG/spec-r10"
check "and it exists" "yes" "$([ -d "$REPO/scratchpad/cp-snap/$TAG/spec-r10" ] && echo yes || echo no)"
printf 'a new line\n' >> "$DIR/0099_fix_t.spec-changes.md"
OUT="$(run spec 10)"
contains "an edit since the last snapshot is counted" "$OUT" '"hunks":1'

echo; echo "T11d. mid-run argument overrides are read, and malformed ones ignored"
printf '{"maxFixGroups":3}' > "$REPO/scratchpad/cp-args/$TAG.json"
OUT="$(run spec 11)"
contains "the override is carried out" "$OUT" '"maxFixGroups":3'
printf '{not json' > "$REPO/scratchpad/cp-args/$TAG.json"
OUT="$(run spec 12 2>/dev/null)"
contains "a malformed override file is ignored, not fatal" "$OUT" '"overrides":{}'
rm -f "$REPO/scratchpad/cp-args/$TAG.json"

echo; echo "T12. it fails rather than proceeding on unknown state"
bash "$SH" --dir "$T/nope" --tag "$TAG" --loop spec --round 1 --repo "$REPO" >/dev/null 2>&1
check "a missing proposal directory exits non-zero" 1 "$?"
bash "$SH" --dir "$DIR" --tag "$TAG" --repo "$REPO" >/dev/null 2>&1
check "missing arguments exit 2" 2 "$?"
printf '# Review log\nno headings here\n' > "$LOG"
printf -- '- FACT: x\n' > "$REPO/scratchpad/cp-log/$TAG/spec.13.a.md"
bash "$SH" --dir "$DIR" --tag "$TAG" --loop spec --round 13 --repo "$REPO" >/dev/null 2>&1
check "a log with no Ledger heading exits non-zero" 1 "$?"

echo
if [ "$fails" -eq 0 ]; then echo "round-boundary.test.sh: all $checks check(s) passed."; exit 0; fi
echo "round-boundary.test.sh: $fails of $checks check(s) FAILED."; exit 1
