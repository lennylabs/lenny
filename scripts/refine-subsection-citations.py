#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Refine a parent-section citation to the numbered subsection it meant.

WHY THIS EXISTS. The line pass converted every citation naming a section and a
line into a bare section reference, and the numbered subsections that would have
given it paragraph
granularity were inserted afterwards, so 737 citations name a 200-line section
where a subsection exists. This restores the granularity from the line numbers
the conversion discarded, which git still holds.

WHY IT IS EXACT RATHER THAN A HEURISTIC. The subsection insertion into
spec/10_gateway-internals.md and spec/13_security-model.md was pure: 8 headings
and 8 blank lines into one, 7 and 7 into the other, with no other addition and
no removal. So a line in the pre-insertion file maps to the same line plus the
inserted lines above it. spec/04_system-components.md does NOT qualify: its
insertion commit also removed 43 lines for the section reduction, so a line
inside the removed block has no successor and the offset does not hold. §4.4 and
§4.7 citations are therefore left at parent level.

THE GUARD THAT MAKES IT SAFE. A citation is refined only when its mapped line
falls inside the bounds of the section it names. Roughly 15 of the 737 already
pointed outside their section before the migration, and for those the old line
number is evidence of nothing. One such citation names §10.1 but its line sits
inside §10.4, so writing a §10.1 subsection there would turn a stale citation
into a confidently wrong one.
Those are reported and left alone.

Usage:
    refine-subsection-citations.py --baseline <rev> [--apply]

Without --apply it reports what it would write and changes nothing.
"""

import argparse
import re
import subprocess
import sys
from collections import defaultdict

TARGETS = {
    "10.1": "spec/10_gateway-internals.md",
    "13.2": "spec/13_security-model.md",
}
EXCLUDE = [":!spec/", ":!proposals", ":!tests/registers"]


def run(*args):
    return subprocess.run(args, capture_output=True, text=True).stdout


def insertion_map(rev, path):
    """old line -> new line, valid only for a pure-insertion diff."""
    diff = run("git", "diff", "-U0", rev, "--", path)
    inserts = []
    for m in re.finditer(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@", diff, re.M):
        old_count = int(m.group(2) or 1)
        new_start, new_count = int(m.group(3)), int(m.group(4) or 1)
        if old_count != 0:
            raise SystemExit(
                f"{path}: {rev} differs by more than insertion at @@ {m.group(0)} — "
                "the offset mapping does not hold, refuse rather than guess"
            )
        inserts.append((new_start, new_count))
    inserts.sort()

    def to_new(old):
        new = old
        for start, count in inserts:
            if start <= new:
                new += count
        return new

    return to_new


def section_bounds(path, parent):
    """(first, last) current lines of the parent section, and its subsections."""
    lines = open(path).read().splitlines()
    depth = parent.count(".") + 1
    start = end = None
    subs = []
    for i, line in enumerate(lines, 1):
        m = re.match(r"^(#{2,6})\s+(\d+(?:\.\d+)*)\s+(.*)", line)
        if not m:
            continue
        num = m.group(2)
        if num == parent:
            start = i
            continue
        if start is not None:
            if num.startswith(parent + ".") and num.count(".") == depth:
                subs.append((i, num, m.group(3)))
            elif not num.startswith(parent + "."):
                end = i - 1
                break
    return start, (end or len(lines)), subs


def owning_subsection(subs, line):
    owner = None
    for ln, num, title in subs:
        if ln <= line:
            owner = (num, title)
        else:
            break
    return owner


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--baseline", required=True, help="rev holding the pre-conversion citations")
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()

    plans = defaultdict(list)
    skipped = []
    for parent, spec in TARGETS.items():
        to_new = insertion_map(args.baseline, spec)
        first, last, subs = section_bounds(spec, parent)
        if not subs:
            raise SystemExit(f"{spec}: no numbered subsections under §{parent}")

        cite = re.compile(r"§" + re.escape(parent) + r"\s+lines?\s+(\d+)")
        # git grep uses POSIX ERE, which has no \s; keep the two patterns separate.
        grep_pat = "§" + re.escape(parent).replace("\\", "") + "[[:space:]]+lines?[[:space:]]+[0-9]+"
        out = run("git", "grep", "-nE", grep_pat, args.baseline, "--", *EXCLUDE)
        for row in out.splitlines():
            m = re.match(r"[^:]+:([^:]+):(\d+):(.*)", row)
            if not m:
                continue
            path, text = m.group(1), m.group(3)
            for c in cite.finditer(text):
                old_line = int(c.group(1))
                new_line = to_new(old_line)
                if not (first <= new_line <= last):
                    skipped.append((path, parent, old_line, "maps outside the section it names"))
                    continue
                owner = owning_subsection(subs, new_line)
                if owner is None:
                    skipped.append((path, parent, old_line, "maps above the first subsection"))
                    continue
                # Anchor on the citation's own line text with the line number
                # stripped, which is what the line pass left behind. Matching by
                # occurrence index instead would drift: a file's bare §X.Y
                # occurrences are not all former line citations, and their order
                # is not the baseline's.
                anchor = cite.sub("§" + parent, text).strip()
                plans[path].append((parent, owner[0], anchor))

    refined = 0
    unanchored = []
    for path, entries in sorted(plans.items()):
        try:
            lines = open(path).read().splitlines(keepends=True)
        except OSError:
            continue
        changed = False
        for parent, target, anchor in entries:
            hit = None
            for i, line in enumerate(lines):
                if line.strip() == anchor and re.search(r"§" + re.escape(parent) + r"(?![\d.])", line):
                    hit = i
                    break
            if hit is None:
                unanchored.append((path, parent, anchor[:60]))
                continue
            lines[hit] = re.sub(r"§" + re.escape(parent) + r"(?![\d.])", "§" + target, lines[hit], count=1)
            changed = True
            refined += 1
        if changed and args.apply:
            open(path, "w").write("".join(lines))

    print(f"files touched:  {len(plans)}")
    print(f"citations refined: {refined}")
    print(f"left at parent level (guard):    {len(skipped)}")
    print(f"left at parent level (no anchor): {len(unanchored)}")
    for path, parent, line, why in skipped[:20]:
        print(f"   §{parent} line {line} in {path}: {why}")
    if not args.apply:
        print("\n(dry run: nothing written; pass --apply to write)")


if __name__ == "__main__":
    sys.exit(main())
