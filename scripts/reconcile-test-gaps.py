#!/usr/bin/env python3
"""Reconcile TEST-GAPS.md after a merge or edit.

The integrator runs this on every integration; a runner or proposal worker
can run it with --check before pushing to catch a problem locally. It does
two integrator-owned jobs that must not be left to the parallel machines:

  1. Renumber duplicate finding IDs. When two machines each file a new
     "discovered" finding under the same section, they pick "next free N"
     independently and collide (git auto-merges both without a conflict
     marker, so the collision is silent). This reassigns the *later*
     occurrence of every duplicated ID to section-max+1, preserving both
     findings.

  2. Recompute the "Across ... audited units" summary line from the actual
     findings, by severity and resolved/open status, so the counts never
     drift. Parallel machines must not edit this line; only this script does.

Finding heading grammar (the only lines counted):
    ### - [ ] T-4.6.9 — Title text [Medium] — OPEN
    ### - [x] T-4.6.9 — Title text [Medium] — RESOLVED <sha>
The ID may contain hyphens (ranges like T-24.0-24.4.1, T-DOC-GS.1); the
separator before the title is a spaced em-dash (" — ").

Usage:
    reconcile-test-gaps.py [--check] [PATH]
        PATH defaults to TEST-GAPS.md in the repo root.
        --check reports problems and exits non-zero without writing.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections import Counter, defaultdict

# A finding heading. Group 1 is the checkbox state, group 2 the ID, group 3
# the severity. The ID is greedy-free (\S+) and terminated by the spaced
# em-dash so hyphenated IDs (ranges, T-DOC-*) are captured whole.
HEADING = re.compile(r"^### - \[( |x)\] (\S+) — .*\[(High|Medium|Low|Info)\]", re.I)
SUMMARY = re.compile(r"^Across .*? audited units: \*\*\d+ findings\*\*")


def split_id(fid: str):
    """Return (section_prefix, trailing_int) or (fid, None) if not numeric."""
    pre, _, last = fid.rpartition(".")
    if pre and last.isdigit():
        return pre, int(last)
    return fid, None


def renumber_duplicates(lines: list[str]) -> list[tuple[str, str]]:
    """Renumber later occurrences of any duplicated finding ID in place.

    Mutates `lines`. Returns the list of (old_id, new_id) reassignments.
    """
    ids = [HEADING.match(l).group(2) for l in lines if HEADING.match(l)]
    dup = {k for k, v in Counter(ids).items() if v > 1}
    if not dup:
        return []

    sec_max: dict[str, int] = defaultdict(int)
    for fid in ids:
        pre, n = split_id(fid)
        if n is not None:
            sec_max[pre] = max(sec_max[pre], n)

    seen: set[str] = set()
    remap: list[tuple[str, str]] = []
    for i, l in enumerate(lines):
        m = HEADING.match(l)
        if not m:
            continue
        fid = m.group(2)
        if fid in dup and fid in seen:
            pre, _ = split_id(fid)
            sec_max[pre] += 1
            new = f"{pre}.{sec_max[pre]}"
            lines[i] = l.replace(fid, new, 1)
            remap.append((fid, new))
        seen.add(fid)
    return remap


def compute_summary(lines: list[str]) -> tuple[str, dict]:
    """Return (summary_line, stats) recomputed from the findings."""
    sev = {"high": 0, "medium": 0, "low": 0, "info": 0}
    res = opn = 0
    for l in lines:
        m = HEADING.match(l)
        if not m:
            continue
        sev[m.group(3).lower()] += 1
        if m.group(1) == "x":
            res += 1
        else:
            opn += 1
    total = res + opn
    line = (
        f"Across 132 audited units: **{total} findings** — "
        f"{sev['high']} High, {sev['medium']} Medium, {sev['low']} Low, "
        f"{sev['info']} Info. {res} are resolved and {opn} remain open."
    )
    return line, {"total": total, **sev, "resolved": res, "open": opn}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("path", nargs="?", default="TEST-GAPS.md")
    ap.add_argument(
        "--check",
        action="store_true",
        help="report problems and exit non-zero without writing",
    )
    args = ap.parse_args()

    with open(args.path) as f:
        text = f.read()
    lines = text.split("\n")

    remap = renumber_duplicates(lines)
    new_summary, stats = compute_summary(lines)

    stale = False
    for i, l in enumerate(lines):
        if SUMMARY.match(l):
            if l != new_summary:
                stale = True
                if not args.check:
                    lines[i] = new_summary
            break

    problems = bool(remap) or stale
    for old, new in remap:
        print(f"duplicate finding ID: {old} -> {new}")
    if stale:
        print("summary line stale; recomputed:")
        print(f"  {new_summary}")

    if args.check:
        if problems:
            print("reconcile --check: problems found (see above)", file=sys.stderr)
            return 1
        print(
            f"reconcile --check: clean "
            f"({stats['total']} findings, {stats['resolved']} resolved)"
        )
        return 0

    if problems:
        with open(args.path, "w") as f:
            f.write("\n".join(lines))
        print(f"reconciled {args.path}: {len(remap)} renumbered, summary refreshed")
    else:
        print(
            f"reconcile: already clean "
            f"({stats['total']} findings, {stats['resolved']} resolved)"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
