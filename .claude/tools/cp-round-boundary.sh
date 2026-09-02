#!/usr/bin/env bash
# The per-round bookkeeping of a change-proposal review loop, as one script.
#
# A workflow script has no filesystem access, so every file operation costs an
# agent invocation. That makes agent COUNT the thing to design against, and the
# rule that follows is: put the mechanical logic in a script the repository
# owns, and give the agent one exact command. The agent becomes a transport
# with no room to deviate, the logic is in version control where it is
# reviewable and testable, and a failure is an exit code rather than an
# instruction an agent quietly skipped at the end of a long prompt.
#
# The state of the tree at the END of round N is the state at the START of
# round N+1 -- nothing runs between them -- so one call does both halves.
#
# Usage:
#   cp-round-boundary.sh --dir <proposal-dir> --tag <runTag> --loop <name>
#                        --round <N> [--repo <root>] [--compact-at <lines>]
#                        [--compact-growth <lines>] [--standing-target <lines>]
#                        [--standing-trigger <lines>]
#
# Prints one JSON object on stdout, on one line, and nothing else:
#   {
#     "merged": <shards folded into the review log>,
#     "ledgerLines": <lines under ## Ledger>,
#     "compactionDue": true|false,
#     "hunksKnown": true|false,   whether "hunks" had a baseline to compare against
#     "drained": <ledger lines moved to Retired after a compaction pass>,
#     "standingTarget": <lines the compaction pass is asked to reach>,
#     "standingTrigger": <lines at which compaction becomes due>,
#     "targetRaises": <times the target has been raised because a pass could not reach it>,
#     "targetRaisedNow": true|false,
#     "changedFiles": [ files that changed since the previous call ],
#     "hunks": <changed hunks against the previous round's snapshot>,
#     "snapshot": "<path to the snapshot this round starts from>"
#   }
#
# Exit non-zero on any failure, so the caller marks the round incomplete rather
# than proceeding on unknown state.

set -uo pipefail

DIR=""; TAG=""; LOOP=""; ROUND=""; REPO=""; COMPACT_AT=2000; COMPACT_GROWTH=400
# The trigger and the target are SEPARATE numbers, and that is the whole point.
# They used to be one: compaction became due at 80 lines and the pass was told to
# reach 80 lines, so the moment a run could not get under 80 it paid for a pass
# every round for the rest of its life. One measured run spent twelve passes
# averaging 700s that way, ending at 376 lines, which is about a fifth of its
# wall clock. A pass that reaches the target now buys real headroom before the
# next one is due.
#
# The numbers are higher than they look because the cost arithmetic runs the
# other way. A 376-line standing context read by every agent every round cost
# that run roughly 4% of its tokens; the twelve passes protecting it cost 21% of
# its wall clock. Carrying the section is cheap and compacting it is not.
STANDING_TARGET=200; STANDING_TRIGGER=320
# How far the target moves when a pass could not reach it, and how much headroom
# the trigger keeps above the target.
TARGET_HEADROOM=40; TRIGGER_HEADROOM=120
# Whether a compaction pass actually RAN since the previous call. Only the caller
# knows: this script can see that it ASKED for one, which is a different claim.
COMPACTED=0
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --loop) LOOP="$2"; shift 2 ;;
    --round) ROUND="$2"; shift 2 ;;
    --repo) REPO="$2"; shift 2 ;;
    --compact-at) COMPACT_AT="$2"; shift 2 ;;
    --standing-target) STANDING_TARGET="$2"; shift 2 ;;
    --standing-trigger) STANDING_TRIGGER="$2"; shift 2 ;;
    --compact-growth) COMPACT_GROWTH="$2"; shift 2 ;;
    --compacted) COMPACTED="$2"; shift 2 ;;
    *) echo "cp-round-boundary: unknown argument $1" >&2; exit 2 ;;
  esac
done
[ -n "$DIR" ] && [ -n "$TAG" ] && [ -n "$LOOP" ] && [ -n "$ROUND" ] || {
  echo "cp-round-boundary: --dir, --tag, --loop and --round are required" >&2; exit 2; }
[ -d "$DIR" ] || { echo "cp-round-boundary: no such proposal directory: $DIR" >&2; exit 1; }

[ -n "$REPO" ] || REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE="$REPO/scratchpad/cp-state/$TAG"
SHARDS="$REPO/scratchpad/cp-log/$TAG"
SNAPS="$REPO/scratchpad/cp-snap/$TAG"
mkdir -p "$STATE" "$SHARDS" "$SNAPS" || { echo "cp-round-boundary: cannot create state dirs" >&2; exit 1; }

# Read an integer from a state file, falling back to a default unless the file
# holds a plain non-negative integer.
#
# `cat` of an EMPTY file SUCCEEDS, so a `|| echo <default>` fallback never fires
# and the guard has to be on the value. That matters because a kill during the
# truncate-then-write of a state file leaves exactly that empty file: before this
# guard, an empty standing-trigger made every `-ge` comparison error to false, so
# compaction never became due again at any size, the file never healed, and the
# script still exited 0 so the caller saw a healthy round. Non-numeric junk was
# worse: it either aborted under `set -u` with no JSON, or emitted correct-looking
# JSON with exit 1 forever.
read_int() {
  local f="$1" dflt="$2" v=""
  [ -f "$f" ] || { printf '%s' "$dflt"; return 0; }
  v=$(cat "$f" 2>/dev/null || printf '')
  v="${v//[[:space:]]/}"
  case "$v" in
    ''|*[!0-9]*) printf '%s' "$dflt" ;;
    *) printf '%s' "$v" ;;
  esac
}

# Write a state file, or fail the round. `set -e` is deliberately not in force
# here, so every state write was unchecked: a state directory that had become
# unwritable produced a full success JSON and exit 0 while the write audit went
# permanently blind and the ledger baseline froze. The header contract says the
# script exits non-zero on any failure, and this is what makes that true of the
# state writes.
write_state() {
  printf '%s\n' "$2" >"$1" || { echo "cp-round-boundary: cannot write $1" >&2; exit 1; }
}

# Escape a string for embedding in a JSON string literal. A proposal file whose
# name contains a quote or a backslash produced invalid JSON on stdout, and the
# caller then could not close the round at all.
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

# The two thresholds are independent operator knobs, so setting only one is a
# plausible mistake, and a trigger at or below the target reinstates the exact
# latch this script exists to remove: compaction becomes due at a size the pass
# is not even asked to get under, so it fires every round forever.
case "$STANDING_TARGET" in ''|*[!0-9]*) echo "cp-round-boundary: --standing-target must be a non-negative integer" >&2; exit 2 ;; esac
case "$STANDING_TRIGGER" in ''|*[!0-9]*) echo "cp-round-boundary: --standing-trigger must be a non-negative integer" >&2; exit 2 ;; esac
if [ "$STANDING_TRIGGER" -le "$STANDING_TARGET" ]; then
  echo "cp-round-boundary: --standing-trigger ($STANDING_TRIGGER) must exceed --standing-target ($STANDING_TARGET); using $((STANDING_TARGET + TRIGGER_HEADROOM))" >&2
  STANDING_TRIGGER=$((STANDING_TARGET + TRIGGER_HEADROOM))
fi

STEM="$(basename "$DIR")"
LOG="$DIR/$STEM.review-log.md"

# ---- 0. Drain the ledger the compaction pass just read --------------------
#
# Compaction is two halves. The AGENT curates: it rewrites `## Standing context`
# from the whole ledger, and never reads `## Retired`. This script then does the
# mechanical half: it moves the ledger it read into `## Retired`, whole.
#
# ORDER MATTERS AND IS WHY THIS BLOCK IS FIRST. The pass runs after the previous
# boundary call, so the move cannot happen in the same call. It happens here, at
# the FOLLOWING boundary, and it must happen BEFORE this round's shards are
# merged below -- otherwise it would sweep up entries the pass never saw. Drain,
# then merge.
#
# Gated on --compacted, which says a pass actually RAN rather than that one was
# asked for. Without that a run killed between the request and the pass would
# have its ledger drained with nothing curated from it.
#
# Entries move WHOLE, keeping their ids. A one-line summary would mean a dead
# agent loses the text, and agents cite ledger entries by id, so a moved entry
# must still resolve. `## Retired` grows without bound, which is free: nothing
# reads it, the compactor included.
drained=0
if [ "$COMPACTED" = "1" ] && [ -f "$LOG" ]; then
  grep -qE '^## Ledger[[:space:]]*$' "$LOG" || {
    echo "cp-round-boundary: $LOG has no '## Ledger' heading on a line of its own" >&2; exit 1; }
  grep -qE '^## Retired[[:space:]]*$' "$LOG" || {
    echo "cp-round-boundary: $LOG has no '## Retired' heading on a line of its own" >&2; exit 1; }
  before_ledger=$(awk '/^## Ledger[[:space:]]*$/{f=1;next} /^## /{f=0} f' "$LOG" | grep -c . || true)
  if [ "$before_ledger" -gt 0 ]; then
    tmp="$LOG.draining"
    awk '
      # Pass 1 collects the ledger body; pass 2 prints the file with the ledger
      # emptied and the body appended to the end of Retired.
      BEGIN { n = 0 }
      FNR == NR {
        if ($0 ~ /^## Ledger[[:space:]]*$/) { inl = 1; next }
        if (inl && $0 ~ /^## /) { inl = 0 }
        if (inl) body[n++] = $0
        next
      }
      { }
      $0 ~ /^## Ledger[[:space:]]*$/ { print; skip = 1; next }
      skip && $0 ~ /^## / { skip = 0 }
      skip { next }
      $0 ~ /^## Retired[[:space:]]*$/ { print; inret = 1; next }
      inret && $0 ~ /^## / {
        for (i = 0; i < n; i++) print body[i]
        inret = 0
        print; next
      }
      { print }
      END { if (inret) for (i = 0; i < n; i++) print body[i] }
    ' "$LOG" "$LOG" >"$tmp" || { rm -f "$tmp"; exit 1; }
    # Nothing may be lost. Every non-blank line of the file must survive the
    # move; the only change is which section holds the ledger body.
    if [ "$(grep -c . "$tmp" || true)" -ne "$(grep -c . "$LOG" || true)" ]; then
      rm -f "$tmp"
      echo "cp-round-boundary: draining the ledger would change the file's line count; refusing" >&2
      exit 1
    fi
    mv "$tmp" "$LOG" || exit 1
    drained=$before_ledger
  fi
fi

# ---- 1. Merge this round's log shards into the review log -----------------
#
# Each parallel agent writes its own shard, because twelve lenses appending to
# one file concurrently lose writes. The merge is idempotent: a shard is
# appended and then deleted, one at a time, so a shard that survives a crash
# mid-merge is one that was not yet appended.
merged=0
if [ -d "$SHARDS" ] && [ -f "$LOG" ]; then
  # Every unmerged shard for this loop, not only this round's. A round whose
  # boundary call failed leaves its shards behind, and selecting by round would
  # orphan them permanently; the round is in the filename for uniqueness, not
  # for selection.
  for shard in $(find "$SHARDS" -maxdepth 1 -name "$LOOP.*.md" 2>/dev/null | sort); do
    [ -s "$shard" ] || { rm -f "$shard"; continue; }
    # Splice under ## Ledger rather than appending to the file, which would put
    # every entry after ## Retired and leave the ledger permanently empty. The
    # insertion point is the end of the Ledger section: just before the next
    # top-level heading, or at EOF when Ledger is last.
    # The guard must use the SAME pattern as the splice below. It used to be
    # looser (`^## Ledger`), so a heading like `## Ledger (open)` passed the
    # guard, matched nothing in the awk, produced an unchanged file, and then
    # the shard was DELETED and counted as merged. That silently destroyed a
    # reviewing agent's entire findings block and reported success.
    grep -qE '^## Ledger[[:space:]]*$' "$LOG" || {
      echo "cp-round-boundary: $LOG has no '## Ledger' heading on a line of its own" >&2; exit 1; }
    tmp="$LOG.merging"
    awk -v shard="$shard" '
      BEGIN { inledger = 0; done = 0 }
      /^## Ledger[[:space:]]*$/ { print; inledger = 1; next }
      inledger && /^## / {
        printf "\n"
        while ((getline line < shard) > 0) print line
        close(shard)
        printf "\n"
        done = 1; inledger = 0
        print; next
      }
      { print }
      END {
        if (inledger && !done) {
          printf "\n"
          while ((getline line < shard) > 0) print line
          close(shard)
        }
      }
    ' "$LOG" >"$tmp" || { rm -f "$tmp"; exit 1; }
    mv "$tmp" "$LOG" || exit 1
    rm -f "$shard"
    merged=$((merged + 1))
  done
fi

# ---- 2. Sizes, for the compaction trigger --------------------------------
#
# The trigger is on the STANDING CONTEXT, not the ledger. Every agent is told to
# read the standing context and nothing else, so that section is the only part
# of the log anyone carries; the ledger is read end to end by exactly one agent,
# the compactor itself. Triggering on ledger size fired an expensive pass to
# protect against a cost that does not exist -- on one run, three compactions
# averaging fifteen minutes each while the section they were protecting sat at
# 92 lines against its 80-line target.
#
# The ledger keeps a bound, far higher, purely so it cannot grow without limit.
ledger_lines=0
standing_lines=0
if [ -f "$LOG" ]; then
  ledger_lines=$(awk '/^## Ledger/{f=1;next} /^## /{f=0} f' "$LOG" | grep -c . || true)
  standing_lines=$(awk '/^## Standing context/{f=1;next} /^## /{f=0} f' "$LOG" | grep -c . || true)
fi
# The one integer state read left unguarded. An empty file is benign here, since
# empty expands to 0 in the arithmetic below -- which is why it was missed -- but
# `$(( ))` resolves a bare word as a VARIABLE NAME, so junk either aborts the
# round under `set -u` with no JSON at all and never heals, or silently injects
# another variable's value into the growth figure.
prev_ledger=$(read_int "$STATE/ledger-lines" 0)
growth=$((ledger_lines - prev_ledger))

# ---- 2b. Adaptive backoff -------------------------------------------------
#
# A target the pass structurally cannot reach is worse than no target: it fires
# a pass every round, each one obeying its instructions and failing an
# arithmetic impossibility. The standing context is an accumulator with one
# drain -- every FACT, WATCHOUT, DECISION and MISTAKE from an aged entry is
# lifted in, MISTAKE keeps its reasoning by instruction, OPEN and UNVERIFIED may
# never be dropped, and only an explicit CORRECTS removes anything -- so what
# it settles at is a property of the run rather than a number chosen in advance.
#
# So the run finds its own level. When a pass could not reach the target, the
# target moves up to what it actually achieved plus headroom, and the trigger
# follows. The count of raises is the signal worth reading: a run that keeps
# raising is accumulating unresolved state faster than it resolves it, which is
# what circling looks like from here.
target=$STANDING_TARGET
trigger=$STANDING_TRIGGER
# The persisted pair is the ADAPTED value, so it is carried forward only while
# the caller's own numbers are unchanged. Letting state win unconditionally
# would mean a caller raising the target mid-run was silently ignored, which is
# the one thing a knob must never do.
base="$STANDING_TARGET:$STANDING_TRIGGER"
prev_base=""
[ -f "$STATE/standing-base" ] && prev_base=$(cat "$STATE/standing-base" 2>/dev/null || printf '')
if [ "$base" = "$prev_base" ]; then
  target=$(read_int "$STATE/standing-target" "$STANDING_TARGET")
  trigger=$(read_int "$STATE/standing-trigger" "$STANDING_TRIGGER")
else
  # The caller changed its own numbers. Any pass in flight was given the OLD
  # target, so judging it against the new one both misattributes it and can
  # raise the target ABOVE the value the caller just asked for -- a request to
  # tighten the budget producing a looser one than before.
  rm -f "$STATE/compaction-pending" || true
fi
# The raise count survives a base change: it counts passes that could not reach
# whatever target stood at the time, which is a run-level signal either way.
raises=$(read_int "$STATE/standing-raises" 0)
# The CLI values were validated above; the pair read back from state was not, so
# a state file holding trigger <= target reinstated the every-round latch.
if [ "$trigger" -le "$target" ]; then trigger=$((target + TRIGGER_HEADROOM)); fi
write_state "$STATE/standing-base" "$base"

caller_gap=$((STANDING_TRIGGER - STANDING_TARGET))
# Floored at 1, not at the default headroom: clamping a NARROW caller gap up to
# the default is the same "silently ignore the knob" failure the wide case has.
# The argument validation above already guarantees trigger > target, so the gap
# is at least 1.
[ "$caller_gap" -lt 1 ] && caller_gap=1

raised_now=false
if [ -f "$STATE/compaction-pending" ]; then
  # WHICH threshold asked for the pass. The ledger backstop also requests one,
  # and attributing its outcome to the standing context ratcheted the standing
  # target up and reported a raise that never happened -- a false signal that
  # reaches every reviewing agent's prompt through the introspection block.
  pending_kind=$(head -1 "$STATE/compaction-pending" 2>/dev/null | awk '{print $1}' || printf '')
  # A pass is judged only when one actually RAN. The marker records that a pass
  # was ASKED for, which is a different claim: a run killed between the request
  # and the pass would otherwise be read as a failed pass, raising the target
  # past the current size and permanently excusing the very section that needed
  # compacting.
  if [ "$COMPACTED" = "1" ] && [ "$pending_kind" = "standing" ] && [ "$standing_lines" -gt "$target" ]; then
    target=$((standing_lines + TARGET_HEADROOM))
    trigger=$((target + caller_gap))
    raises=$((raises + 1))
    raised_now=true
  fi
  # Consumed either way: a new marker is written below if compaction is still
  # due, so a crashed round re-requests rather than carrying a stale judgement.
  rm -f "$STATE/compaction-pending" || true
fi

# Decay, so the ratchet is not one-way. Without it a single failed pass held the
# target up for the rest of the run even after the section shrank back, and one
# bad round disabled compaction permanently. The target follows the section down
# as well as up, and never below the number the caller asked for.
# The caller's own gap between target and trigger is preserved through both the
# raise and the decay. Recomputing the trigger from the default headroom threw
# away a deliberately wide trigger: an operator asking for 200/600 got 200/320
# back after one raise-and-decay cycle, so compaction fired three times as often
# as they asked -- the every-round latch, reintroduced through the decay path.

if [ "$raised_now" = "false" ] && [ "$target" -gt "$STANDING_TARGET" ]; then
  want=$((standing_lines + TARGET_HEADROOM))
  [ "$want" -lt "$STANDING_TARGET" ] && want=$STANDING_TARGET
  if [ "$want" -lt "$target" ]; then
    target=$want
    trigger=$((target + caller_gap))
  fi
fi

compaction_due=false
pending_write=""
if [ "$standing_lines" -ge "$trigger" ]; then
  compaction_due=true
  pending_write="standing $standing_lines"
elif [ "$ledger_lines" -ge "$COMPACT_AT" ]; then
  compaction_due=true
  pending_write="ledger $ledger_lines"
fi
[ -n "$pending_write" ] && write_state "$STATE/compaction-pending" "$pending_write"
write_state "$STATE/standing-target" "$target"
write_state "$STATE/standing-trigger" "$trigger"
write_state "$STATE/standing-raises" "$raises"
write_state "$STATE/ledger-lines" "$ledger_lines"

# ---- 3. The write audit --------------------------------------------------
#
# Which files changed since the previous call. The loop knows which its fixer
# was allowed to touch; anything else that moved is reported. This is not
# appended to a reviewing agent's prompt on purpose: a skipped audit fails
# silently and takes a guarantee with it.
changed="[]"
HASHES="$STATE/hashes-$LOOP"
new_hashes="$(cd "$DIR" && find . -maxdepth 1 -name '*.md' -exec md5sum {} + 2>/dev/null | sort -k2)"
if [ -f "$HASHES" ]; then
  # `awk '{print $3}'` yields an EMPTY field when a diff line has no third
  # column, which happens when either side of the comparison is empty, so the
  # audit reported a changed file named "" that does not exist. NF>=3 drops
  # those. The gsub pair escapes a backslash and then a quote in the name: an
  # unescaped one produced invalid JSON on stdout and the caller could not close
  # the round at all.
  changed=$(diff <(cat "$HASHES") <(printf '%s\n' "$new_hashes") 2>/dev/null \
    | grep '^[<>]' | awk 'NF>=3 {print $3}' | sed 's|^\./||' | sort -u \
    | awk 'BEGIN{printf "["; n=0}
           length($0) > 0 {
             s=$0; gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s)
             printf "%s\"%s\"", (n++ ? "," : ""), s
           }
           END{printf "]"}')
  [ -n "$changed" ] || changed="[]"
fi
write_state "$HASHES" "$new_hashes"

# ---- 4. Snapshot the tree the NEXT round reads ---------------------------
PREV="$SNAPS/$LOOP-r$((ROUND))"
NEXT="$SNAPS/$LOOP-r$((ROUND + 1))"
hunks=0
# Whether `hunks` MEANS anything. On the first round of a loop there is no
# previous snapshot to diff against, so zero is the absence of a baseline rather
# than the absence of change. A caller that reads the two alike concludes the
# round's fixer edited nothing, which is what happened: a measured run withdrew
# every genuine fix from round 1 of both loops and reported nothing fixed.
hunks_known=false
if [ -d "$PREV" ]; then
  hunks_known=true
  hunks=$(diff -ru "$PREV" "$DIR" 2>/dev/null | grep -c '^@@' || true)
fi
rm -rf "$NEXT" && cp -r "$DIR" "$NEXT" || { echo "cp-round-boundary: snapshot failed" >&2; exit 1; }

# ---- 5. The caller's mid-run argument overrides --------------------------
OVERRIDES="{}"
OV_FILE="$REPO/scratchpad/cp-args/$TAG.json"
if [ -f "$OV_FILE" ]; then
  # Re-emitted through JSON.stringify rather than spliced in as the file's own
  # bytes. An operator hand-writing this file pretty-prints it, which is the
  # documented way to change a knob mid-run, and a measured run of that made
  # stdout four lines with no closing brace on the first. The calling prompt
  # asks the agent for "that line" verbatim, so the agent returned line 1, the
  # workflow's /\{[\s\S]*\}/ matched nothing, and every round closed
  # INCONCLUSIVE for as long as the file existed. The parse is also the validity
  # check, and the path goes through argv so a quote in it cannot rewrite the
  # program. The non-object guard is here because a spliced array or bare number
  # reached applyOverrides, where Object.entries turned it into index keys the
  # log then reported as refused overrides.
  ov=$(node -e '
    const v = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
    if (v === null || typeof v !== "object" || Array.isArray(v)) throw new Error("not an object");
    process.stdout.write(JSON.stringify(v));
  ' "$OV_FILE" 2>/dev/null)
  if [ -n "$ov" ]; then
    OVERRIDES="$ov"
  else
    echo "cp-round-boundary: $OV_FILE is not a JSON object; ignoring it" >&2
  fi
fi

printf '{"merged":%d,"ledgerLines":%d,"standingLines":%d,"ledgerGrowth":%d,"compactionDue":%s,"hunksKnown":%s,"drained":%d,"standingTarget":%d,"standingTrigger":%d,"targetRaises":%d,"targetRaisedNow":%s,"changedFiles":%s,"hunks":%d,"snapshot":"%s","overrides":%s}\n' \
  "$merged" "$ledger_lines" "$standing_lines" "$growth" "$compaction_due" "$hunks_known" "$drained" "$target" "$trigger" "$raises" "$raised_now" "$changed" "$hunks" "$(json_escape "$NEXT")" "$OVERRIDES"
