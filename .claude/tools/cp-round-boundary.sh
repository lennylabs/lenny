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
# Prints one JSON object on stdout and nothing else:
#   {
#     "merged": <shards folded into the review log>,
#     "ledgerLines": <lines under ## Ledger>,
#     "compactionDue": true|false,
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

STEM="$(basename "$DIR")"
LOG="$DIR/$STEM.review-log.md"

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
    grep -q '^## Ledger' "$LOG" || { echo "cp-round-boundary: $LOG has no '## Ledger' heading" >&2; exit 1; }
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
prev_ledger=0
[ -f "$STATE/ledger-lines" ] && prev_ledger=$(cat "$STATE/ledger-lines" 2>/dev/null || echo 0)
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
raises=0
# The persisted pair is the ADAPTED value, so it is carried forward only while
# the caller's own numbers are unchanged. Letting state win unconditionally
# would mean a caller raising the target mid-run was silently ignored, which is
# the one thing a knob must never do.
base="$STANDING_TARGET:$STANDING_TRIGGER"
prev_base=""
[ -f "$STATE/standing-base" ] && prev_base=$(cat "$STATE/standing-base" 2>/dev/null || echo "")
if [ "$base" = "$prev_base" ]; then
  [ -f "$STATE/standing-target" ] && target=$(cat "$STATE/standing-target" 2>/dev/null || echo "$STANDING_TARGET")
  [ -f "$STATE/standing-trigger" ] && trigger=$(cat "$STATE/standing-trigger" 2>/dev/null || echo "$STANDING_TRIGGER")
fi
# The raise count survives a base change: it counts passes that could not reach
# whatever target stood at the time, which is a run-level signal either way.
[ -f "$STATE/standing-raises" ] && raises=$(cat "$STATE/standing-raises" 2>/dev/null || echo 0)
printf '%s\n' "$base" >"$STATE/standing-base"

raised_now=false
if [ -f "$STATE/compaction-pending" ]; then
  # A compaction was asked for at the previous boundary and has since run. If
  # the section is still over the target, the pass could not reach it.
  if [ "$standing_lines" -gt "$target" ]; then
    target=$((standing_lines + TARGET_HEADROOM))
    trigger=$((target + TRIGGER_HEADROOM))
    raises=$((raises + 1))
    raised_now=true
  fi
  rm -f "$STATE/compaction-pending"
fi

compaction_due=false
if [ "$standing_lines" -ge "$trigger" ] || [ "$ledger_lines" -ge "$COMPACT_AT" ]; then
  compaction_due=true
  echo "$ROUND" >"$STATE/compaction-pending"
fi
printf '%s\n' "$target" >"$STATE/standing-target"
printf '%s\n' "$trigger" >"$STATE/standing-trigger"
printf '%s\n' "$raises" >"$STATE/standing-raises"
echo "$ledger_lines" >"$STATE/ledger-lines"

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
  changed=$(diff <(cat "$HASHES") <(printf '%s\n' "$new_hashes") 2>/dev/null \
    | grep '^[<>]' | awk '{print $3}' | sed 's|^\./||' | sort -u \
    | awk 'BEGIN{printf "["} {printf "%s\"%s\"", (NR>1?",":""), $0} END{printf "]"}')
  [ -n "$changed" ] || changed="[]"
fi
printf '%s\n' "$new_hashes" >"$HASHES"

# ---- 4. Snapshot the tree the NEXT round reads ---------------------------
PREV="$SNAPS/$LOOP-r$((ROUND))"
NEXT="$SNAPS/$LOOP-r$((ROUND + 1))"
hunks=0
if [ -d "$PREV" ]; then
  hunks=$(diff -ru "$PREV" "$DIR" 2>/dev/null | grep -c '^@@' || true)
fi
rm -rf "$NEXT" && cp -r "$DIR" "$NEXT" || { echo "cp-round-boundary: snapshot failed" >&2; exit 1; }

# ---- 5. The caller's mid-run argument overrides --------------------------
OVERRIDES="{}"
OV_FILE="$REPO/scratchpad/cp-args/$TAG.json"
if [ -f "$OV_FILE" ]; then
  if node -e "JSON.parse(require('fs').readFileSync('$OV_FILE','utf8'))" >/dev/null 2>&1; then
    OVERRIDES="$(cat "$OV_FILE")"
  else
    echo "cp-round-boundary: $OV_FILE is not valid JSON; ignoring it" >&2
  fi
fi

printf '{"merged":%d,"ledgerLines":%d,"standingLines":%d,"ledgerGrowth":%d,"compactionDue":%s,"standingTarget":%d,"standingTrigger":%d,"targetRaises":%d,"targetRaisedNow":%s,"changedFiles":%s,"hunks":%d,"snapshot":"%s","overrides":%s}\n' \
  "$merged" "$ledger_lines" "$standing_lines" "$growth" "$compaction_due" "$target" "$trigger" "$raises" "$raised_now" "$changed" "$hunks" "$NEXT" "$OVERRIDES"
