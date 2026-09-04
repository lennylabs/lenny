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
OUT="$(run spec 6 --compact-at 100000 --standing-trigger 100000)"
contains "not due below both thresholds" "$OUT" '"compactionDue":false'
contains "standing lines are reported" "$OUT" '"standingLines":'
OUT="$(run spec 7 --compact-at 100000 --standing-target 0 --standing-trigger 1)"
contains "due on standing-context size" "$OUT" '"compactionDue":true'
# A long ledger alone does NOT trigger: nothing but the compactor reads it, so
# firing an expensive pass on its length protects against a cost that does not
# exist. It keeps a backstop bound, far higher.
fresh_log
for i in $(seq 1 60); do printf -- "- FACT: line %s\n" "$i" >> "$REPO/scratchpad/cp-log/$TAG/spec.8.g.md"; done
OUT="$(run spec 8 --compact-at 100000 --standing-trigger 100000)"
contains "a long ledger alone does not trigger it" "$OUT" '"compactionDue":false'
OUT="$(run spec 9 --compact-at 1 --standing-trigger 100000)"
contains "but the ledger backstop still can" "$OUT" '"compactionDue":true'

echo; echo "T11e. the target and the trigger are separate, and the target backs off"
# Fresh adaptation state: earlier blocks left a pending-compaction marker and a
# persisted pair behind, and this block is about how a run adapts from scratch.
rm -f "$REPO/scratchpad/cp-state/$TAG"/standing-* "$REPO/scratchpad/cp-state/$TAG"/compaction-pending
# A standing context well over the target, so a compaction that "runs" between
# two boundary calls cannot have reached it.
{
  printf '# Review log\n\n## Standing context\n'
  for i in $(seq 1 40); do printf -- "- FACT: standing %s\n" "$i"; done
  printf '\n## Ledger\n\n## Retired\nold\n'
} > "$LOG"
OUT="$(run spec 20 --standing-target 5 --standing-trigger 10)"
contains "the target is reported" "$OUT" '"standingTarget":'
contains "the trigger is reported separately" "$OUT" '"standingTrigger":'
contains "no raise before a compaction has run" "$OUT" '"targetRaisedNow":false'
contains "compaction is due over the trigger" "$OUT" '"compactionDue":true'
# --compacted 1 is the caller saying a pass actually RAN. The script can only see
# that it ASKED for one, which is a different claim.
OUT="$(run spec 21 --standing-target 5 --standing-trigger 10 --compacted 1)"
contains "the target is raised" "$OUT" '"targetRaisedNow":true'
contains "and the raise is counted" "$OUT" '"targetRaises":1'
# Having backed off, the run is no longer immediately due again: that is the
# latch this change removes.
OUT="$(run spec 22 --standing-target 5 --standing-trigger 10)"
contains "and it is no longer due every round" "$OUT" '"compactionDue":false'
contains "the raise count does not climb without cause" "$OUT" '"targetRaises":1'


echo; echo "T11f. the backoff does not act on claims it cannot support"
STATEDIR="$REPO/scratchpad/cp-state/$TAG"
# A pass REQUESTED but never RUN is not a failed pass. A run killed between the
# two is routine, and treating them alike raised the target past the current
# size and permanently excused the one section that needed compacting.
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
OUT="$(run spec 30 --standing-target 5 --standing-trigger 10)"
contains "a pass is requested" "$OUT" '"compactionDue":true'
OUT="$(run spec 31 --standing-target 5 --standing-trigger 10)"
contains "no raise when no pass ran" "$OUT" '"targetRaisedNow":false'
contains "and the section is still due" "$OUT" '"compactionDue":true'

# The marker must CLEAR once consumed, or the target raises every round forever.
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
OUT="$(run spec 40 --standing-target 5 --standing-trigger 10)"
OUT="$(run spec 41 --standing-target 5 --standing-trigger 10 --compacted 1)"
contains "the first failed pass raises once" "$OUT" '"targetRaises":1'
OUT="$(run spec 42 --standing-target 5 --standing-trigger 10 --compacted 1)"
contains "a consumed marker does not raise again" "$OUT" '"targetRaisedNow":false'
contains "and the count stays put" "$OUT" '"targetRaises":1'

# A ledger-backstop pass says nothing about the standing context, so its outcome
# must not ratchet the standing target nor report a raise into the introspection
# prompt every reviewing agent reads.
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
# A ledger with content and a standing context well UNDER its trigger, so only
# the backstop can be what fires.
{
  printf '# Review log\n\n## Standing context\n- FACT: short\n\n## Ledger\n'
  for i in $(seq 1 5); do printf -- "- entry %s\n" "$i"; done
  printf '\n## Retired\nold\n'
} > "$LOG"
OUT="$(run spec 43 --standing-target 200 --standing-trigger 320 --compact-at 1)"
contains "the ledger backstop fires" "$OUT" '"compactionDue":true'
# The standing context must be ABOVE the target here, or the size check
# short-circuits and `pending_kind` is never the discriminator -- which is how
# this test passed while the ledger/standing attribution was broken.
{
  printf '# Review log\n\n## Standing context\n'
  for i in $(seq 1 30); do printf -- "- FACT: standing %s\n" "$i"; done
  printf '\n## Ledger\n'
  for i in $(seq 1 5); do printf -- "- entry %s\n" "$i"; done
  printf '\n## Retired\nold\n'
} > "$LOG"
OUT="$(run spec 44 --standing-target 5 --standing-trigger 400 --compact-at 1 --compacted 1)"
contains "a ledger pass does not raise the standing target" "$OUT" '"targetRaisedNow":false'
contains "nor the raise count" "$OUT" '"targetRaises":0'
contains "and the target is untouched" "$OUT" '"standingTarget":5'

echo; echo "T11g. corrupt state fails safe instead of wedging the run"
# cat of an EMPTY file SUCCEEDS, so a `|| echo <default>` fallback never fires.
# An empty trigger made every comparison error to false: compaction never became
# due again at any size, the file never healed, and the script still exited 0.
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
: > "$STATEDIR/standing-trigger"
: > "$STATEDIR/standing-target"
printf 'not-a-number\n' > "$STATEDIR/standing-raises"
if OUT="$(run spec 45 --standing-target 5 --standing-trigger 10)"; then rc=0; else rc=$?; fi
check "an empty state file does not wedge the round" "0" "$rc"
contains "the default is used instead" "$OUT" '"standingTrigger":10'
contains "and junk does not corrupt the count" "$OUT" '"targetRaises":0'

echo; echo "T11h. a trigger at or below the target is refused, not obeyed"
# Independent knobs, so raising only the target is a plausible operator move --
# and it used to reinstate the every-round latch this section exists to remove.
OUT="$(run spec 46 --standing-target 400 --standing-trigger 320 2>/dev/null)"
contains "the trigger is lifted above the target" "$OUT" '"standingTrigger":520'
contains "rather than firing every round" "$OUT" '"compactionDue":false'

echo; echo "T11i. a shard is never deleted without being merged"
# The guard used to be looser than the splice, so a heading with trailing text
# passed the guard, matched nothing, and the shard was deleted as "merged" --
# destroying a reviewing agent's whole findings block and reporting success.
printf '# Review log\n\n## Standing context\n\n## Ledger (open)\n\n## Retired\n' > "$LOG"
printf -- '- FACT: precious\n' > "$REPO/scratchpad/cp-log/$TAG/spec.50.g.md"
if OUT="$(run spec 50 2>/dev/null)"; then rc=0; else rc=$?; fi
check "a malformed Ledger heading fails the round" "1" "$rc"
check "and the shard survives" "yes" "$([ -f "$REPO/scratchpad/cp-log/$TAG/spec.50.g.md" ] && echo yes || echo no)"
rm -f "$REPO/scratchpad/cp-log/$TAG/spec.50.g.md"
fresh_log

echo; echo "T11j. the target decays, and keeps the caller's own trigger gap"
STATEDIR="$REPO/scratchpad/cp-state/$TAG"
mk_standing() {
  { printf '# Review log\n\n## Standing context\n'
    for i in $(seq 1 "$1"); do printf -- "- FACT: s %s\n" "$i"; done
    printf '\n## Ledger\n\n## Retired\nold\n'; } > "$LOG"
}
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
# The caller asks for a gap of its own: target 10, trigger 40.
mk_standing 60
OUT="$(run spec 60 --standing-target 10 --standing-trigger 40)"
contains "a run over its trigger becomes due" "$OUT" '"compactionDue":true'
OUT="$(run spec 61 --standing-target 10 --standing-trigger 40 --compacted 1)"
contains "a failed pass raises the target" "$OUT" '"targetRaisedNow":true'
contains "and the trigger keeps the caller's own gap" "$OUT" '"standingTrigger":130'
# The section shrinks: the target must come back DOWN rather than stay ratcheted,
# or one bad round disables compaction for the rest of the run.
mk_standing 12
OUT="$(run spec 62 --standing-target 10 --standing-trigger 40)"
contains "the target decays as the section shrinks" "$OUT" '"standingTarget":52'
contains "and the gap is still the caller's" "$OUT" '"standingTrigger":82'
mk_standing 1
OUT="$(run spec 63 --standing-target 10 --standing-trigger 40)"
contains "decay keeps following the section down" "$OUT" '"standingTarget":41'
contains "still at the caller's gap" "$OUT" '"standingTrigger":71'

echo; echo "T11k. a stored pair that violates the ordering is repaired on read"
rm -f "$STATEDIR"/compaction-pending
printf '400\n' > "$STATEDIR/standing-target"
printf '300\n' > "$STATEDIR/standing-trigger"
printf '10:40\n' > "$STATEDIR/standing-base"
mk_standing 350
OUT="$(run spec 64 --standing-target 10 --standing-trigger 40)"
contains "a stored trigger below its target does not fire every round" "$OUT" '"compactionDue":false'
fresh_log

echo; echo "T11a. stale pass history is evacuated from the change files, lazily and once"
ARCH2="$DIR/0099_fix_t.review-log-archive.md"
SPECF="$DIR/0099_fix_t.spec-changes.md"
NONF="$DIR/0099_fix_t.non-spec-changes.md"
rm -f "$ARCH2"
{ printf '# Spec changes\n\n## 5. Proposed changes\n\nSPEC-1 lands a thing.\n\n'
  printf '## Resolved in adversarial review\n\n### Pass 1 (2026-01-01, automated)\n\n- **A thing was wrong.** It is fixed.\n'; } > "$SPECF"
{ printf '# Non-spec changes\n\n## 8. Testing\n\nA test.\n\n'
  printf '## 11. Resolved in adversarial review\n\n- **Numbered heading, legacy style.** Also fixed.\n'; } > "$NONF"
OUT="$(run spec 80)"
contains "the evacuation is reported" "$OUT" '"evacuated":'
check "the spec file keeps its staged changes" "yes" "$(grep -q 'SPEC-1 lands a thing' "$SPECF" && echo yes || echo no)"
check "and loses the pass history" "0" "$(grep -c 'Resolved in adversarial review' "$SPECF" || true)"
check "the non-spec file loses its NUMBERED heading too" "0" "$(grep -c 'Resolved in adversarial review' "$NONF" || true)"
check "and keeps its own content" "yes" "$(grep -q '## 8. Testing' "$NONF" && echo yes || echo no)"
check "the archive now holds both histories" "2" "$(grep -c '^## Retired from' "$ARCH2")"
check "the pass bodies survive, not just the headings" "yes" "$(grep -q -- '- \*\*A thing was wrong\.\*\* It is fixed\.' "$ARCH2" && grep -q 'Numbered heading, legacy style' "$ARCH2" && echo yes || echo no)"
check "the archive names which file each came from" "yes" "$(grep -q 'spec-changes.md' "$ARCH2" && grep -q 'non-spec-changes.md' "$ARCH2" && echo yes || echo no)"

# Idempotent: a second boundary finds nothing left to move.
OUT="$(run spec 81)"
contains "a second call evacuates nothing" "$OUT" '"evacuated":0'
check "and does not add another archive block" "2" "$(grep -c '^## Retired from' "$ARCH2")"

# A proposal that never had the section is untouched.
printf '# Spec changes\n\n## 5. Proposed changes\n\nOnly staged edits here.\n' > "$SPECF"
before="$(md5sum < "$SPECF")"
OUT="$(run spec 82)"
contains "a clean proposal evacuates nothing" "$OUT" '"evacuated":0'
check "and its file is byte-identical" "$before" "$(md5sum < "$SPECF")"
rm -f "$ARCH2"
fresh_log

echo; echo "T10c2. the script writes the loop state itself, from an argument"
# It used to be a heredoc the calling agent ran as a SECOND command. That shape
# was classified as unsafe and the agent was blocked before it ran, so the state
# was never written and the loop could never certify a round.
STATEDIR2="$REPO/scratchpad/cp-state/$TAG"
rm -rf "$STATEDIR2"
OUT="$(run spec 30 --state-json '{"loop":"spec","round":30,"note":"it'"'"'s quoted"}')"
check "the state file is written" "yes" "$([ -f "$STATEDIR2/state-spec.json" ] && echo yes || echo no)"
check "with the content it was given" "yes" \
  "$(grep -q '"round":30' "$STATEDIR2/state-spec.json" && echo yes || echo no)"
check "an apostrophe survives the shell" "yes" \
  "$(grep -q "it's quoted" "$STATEDIR2/state-spec.json" && echo yes || echo no)"
check "the state is named for the loop" "yes" \
  "$([ -f "$STATEDIR2/state-spec.json" ] && echo yes || echo no)"
# A run that passes none still works: the flag is optional.
OUT="$(run non-spec 31)"
contains "no state argument is not an error" "$OUT" '"merged"'
check "and no state file is invented" "no" \
  "$([ -f "$STATEDIR2/state-non-spec.json" ] && echo yes || echo no)"
rm -rf "$STATEDIR2"

echo; echo "T10d. a shard named outside the merge convention is counted and named, not lost"
# The merge selects by "$LOOP.*.md". A shard an agent named anything else is
# invisible to EVERY round, is never merged, and used to be reported nowhere at
# all, so a whole findings block vanished in silence.
printf -- '### [spec.1.ok.1]\n- FACT: merged fine\n' > "$REPO/scratchpad/cp-log/$TAG/spec.1.ok.md"
printf -- '### [verify.x.1]\n- FACT: invented path\n' > "$REPO/scratchpad/cp-log/$TAG/verify.open-decisions.SPEC-2.md"
OUT="$(run spec 20)"
contains "the conforming shard still merges" "$OUT" '"merged":1'
contains "the stray one is counted" "$OUT" '"strayShards":1'
contains "and named, so it can be found" "$OUT" 'verify.open-decisions.SPEC-2.md'
check "the stray file is NOT deleted" "yes" \
  "$([ -f "$REPO/scratchpad/cp-log/$TAG/verify.open-decisions.SPEC-2.md" ] && echo yes || echo no)"
check "its content is not in the log" "0" \
  "$(grep -c 'invented path' "$LOG" || true)"
rm -f "$REPO/scratchpad/cp-log/$TAG/verify.open-decisions.SPEC-2.md"

echo; echo "T10e. every convention-conforming loop name is recognised, including the rechecks"
# The ordinals matter: recheckName() numbers the second pair onward, so a run at
# the shipped maxRecheckPairs of 2 creates spec-recheck-2 and non-spec-recheck-2.
for L in spec non-spec spec-recheck non-spec-recheck spec-recheck-2 non-spec-recheck-2 spec-recheck-3; do
  printf -- "### [$L.1.z.1]\n- FACT: $L\n" > "$REPO/scratchpad/cp-log/$TAG/$L.1.z.md"
done
OUT="$(run spec 21)"
contains "a spec shard merges and no lane is called stray" "$OUT" '"strayShards":0'
rm -f "$REPO/scratchpad/cp-log/$TAG"/*.md
fresh_log

echo; echo "T11l. the ledger drains to a SEPARATE archive file after a pass, and only then"
STATEDIR="$REPO/scratchpad/cp-state/$TAG"
ARCH="$DIR/0099_fix_t.review-log-archive.md"
rm -f "$STATEDIR"/standing-* "$STATEDIR"/compaction-pending
mk_log() {
  rm -f "$ARCH"
  { printf '# Review log\n\n## Standing context\n- FACT: standing\n\n## Ledger\n'
    printf -- '### [spec.1.review-citations.1]\n- FACT: alpha\n- OPEN: beta\n'
    printf -- '### [spec.2.fix-G1.1]\n- DECISION: gamma\n'
    printf '\n## Retired\nold entry\n'; } > "$LOG"
}

# Without --compacted nothing moves: the pass was asked for, not run.
mk_log
OUT="$(run spec 70)"
contains "no drain without a pass" "$OUT" '"drained":0'
check "the ledger still holds its entries" "2" "$(grep -c '^### \[' "$LOG")"
check "and no archive is created" "no" "$([ -f "$ARCH" ] && echo yes || echo no)"

# With --compacted the ledger AND any pre-existing Retired section move out.
mk_log
OUT="$(run spec 71 --compacted 1)"
contains "the drain reports what it moved" "$OUT" '"drained":6'
check "the archive now exists" "yes" "$([ -f "$ARCH" ] && echo yes || echo no)"
check "the ledger is empty" "0" "$(awk '/^## Ledger/{f=1;next} /^## /{f=0} f' "$LOG" | grep -c . || true)"
check "the log has no Retired section at all" "0" "$(grep -c '^## Retired' "$LOG" || true)"
check "both entries are in the ARCHIVE" "2" "$(grep -c '^### \[' "$ARCH")"
check "the ids survive" "yes" "$(grep -q 'spec.1.review-citations.1' "$ARCH" && grep -q 'spec.2.fix-G1.1' "$ARCH" && echo yes || echo no)"
check "the entry BODIES survive, not just headings" "yes" "$(grep -q -- '- OPEN: beta' "$ARCH" && grep -q -- '- DECISION: gamma' "$ARCH" && echo yes || echo no)"
check "a pre-existing Retired section is carried over too" "yes" "$(grep -q 'old entry' "$ARCH" && echo yes || echo no)"
check "standing context is untouched" "yes" "$(grep -q -- '- FACT: standing' "$LOG" && echo yes || echo no)"
check "the archive is NOT the review log" "no" "$(grep -q -- '- FACT: standing' "$ARCH" && echo yes || echo no)"

# A second drain appends rather than replacing.
printf -- '### [spec.4.review-fresh.1]\n- FACT: later\n' > "$REPO/scratchpad/cp-log/$TAG/spec.4.fresh.md"
OUT="$(run spec 72)"                       # merge the shard, no drain
OUT="$(run spec 73 --compacted 1)"         # now drain it
check "the earlier entries are still archived" "yes" "$(grep -q 'spec.1.review-citations.1' "$ARCH" && echo yes || echo no)"
check "and the later one joins them" "yes" "$(grep -q 'spec.4.review-fresh.1' "$ARCH" && echo yes || echo no)"

# The drain runs BEFORE this round's shards merge, so a fresh shard is not
# archived by a pass that never read it.
mk_log
printf -- '### [spec.9.review-fresh.1]\n- FACT: brand new\n' > "$REPO/scratchpad/cp-log/$TAG/spec.9.fresh.md"
OUT="$(run spec 74 --compacted 1)"
contains "the new shard still merged" "$OUT" '"merged":1'
check "and it is in the LEDGER, not archived" "1" "$(awk '/^## Ledger/{f=1;next} /^## /{f=0} f' "$LOG" | grep -c '^### \[')"
check "the older entries did drain" "yes" "$(grep -q 'spec.1.review-citations.1' "$ARCH" && echo yes || echo no)"
rm -f "$ARCH"
fresh_log

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
# The consumer takes stdout and matches /\{[\s\S]*\}/ against the ONE line it
# was told to reply with, so a pretty-printed override file -- what an operator
# hand-writing one produces -- must not split the object across lines.
printf '{\n  "maxFixGroups": 3,\n  "skipExpansion": true\n}\n' > "$REPO/scratchpad/cp-args/$TAG.json"
OUT="$(run spec 13)"
check "a pretty-printed override file still prints exactly one line" 1 \
  "$(printf '%s\n' "$OUT" | wc -l | tr -d ' ')"
check "and that line alone carries the override to the caller" "3" \
  "$(printf '%s' "$(printf '%s\n' "$OUT" | head -1)" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const m=s.match(/\{[\s\S]*\}/);process.stdout.write(m?String(JSON.parse(m[0]).overrides.maxFixGroups):"none")})' 2>/dev/null)"
printf '["not","an object"]' > "$REPO/scratchpad/cp-args/$TAG.json"
OUT="$(run spec 14 2>/dev/null)"
contains "a JSON array override file is ignored, not spliced" "$OUT" '"overrides":{}'
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
