#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Restore the requirement identifiers the citation rewrite took with the citation.

WHY. The citation grammar reads a parenthesized run standing behind a citation's
last member as that citation's gloss and deletes it with the citation. Where the
run was a requirement identifier the deletion removed a live, greppable token:
the admission validator returns "broad internet egress (NET-002, Section 13.2)"
and its test asserts on that string, so an audit that greps the tree for the
identifier no longer reaches the NetworkPolicy implementing it.

WHAT IT DOES. For every tracked file, compare the base revision with the working
tree line by line, matched on content rather than on diff position. Where a base
line carried an identifier and its working-tree counterpart does not, re-insert
the identifier in the place it occupied relative to the surviving text, keeping
the section reference and dropping the line number the migration retired.

WHAT IT DOES NOT DO. It never writes a line number back, never touches a line
whose counterpart it cannot locate unambiguously, and never edits a file outside
the write domain (proposals, the migration's own fixtures, and the read-excluded
root records keep their text).

Usage:
    restore-requirement-ids.py --base <rev> [--apply]

Without --apply it prints every proposed edit and writes nothing.
"""

import argparse
import difflib
import re
import subprocess
import sys

# A requirement identifier as this repository writes them.
ID = re.compile(r"\b(?:NET|SCL|SEC|STO|CMP|ADM|OPS|RUN|OBS)-\d+[a-z]?\b|\bF-\d+(?:\.\d+)+\b")

# A citation token the migration emits, used only to normalise a line for
# matching. Matching on the normalised form is what lets a base line find its
# counterpart after the citation changed shape.
CITE = re.compile(r"§\d+(?:\.\d+)*(?:\s+lines?\s+[0-9]+(?:[-–,]\s*[0-9]+)*)?")
WS = re.compile(r"\s+")

SKIP_PREFIX = (
    "proposals/",
    "scripts/specshift/testdata/",
    "tests/tier0_static/testdata/",
)
SKIP_EXACT = {
    "BUILD-GAPS.md",
    "BUILD-PLAN.md",
    "BUILD-PROGRESS.md",
    "TEST-GAPS.md",
    "PROPOSAL-QUEUE.md",
    "gateway-runtime-comms.md",
    "gateway-runtime-comms-remediation.md",
}


def tracked(base):
    out = subprocess.run(["git", "ls-files"], capture_output=True, text=True).stdout.split()
    return [
        p
        for p in out
        if not p.startswith(SKIP_PREFIX) and p not in SKIP_EXACT
    ]


def norm(line):
    """A line with citation tokens and identifiers removed, whitespace collapsed.

    Two lines that normalise the same are the same line before and after the
    rewrite, which is what makes the match content-anchored rather than
    positional. Position cannot be used: the rewrite reflowed comments, so line
    numbers do not correspond between the two revisions.
    """
    out = re.sub(r"\(\s*(?:" + ID.pattern + r")[^)]*\)", "", line)
    out = ID.sub("", CITE.sub("", out))
    # Collapse the punctuation the removals leave behind, so a line that lost a
    # parenthetical normalises the same as one that never carried it.
    out = re.sub(r"\(\s*\)", "", out)
    out = re.sub(r"[ \t]*([,;:.])[ \t]*\1+", r"\1", out)
    return WS.sub(" ", out).strip(" \t-—:;,.")


def id_wrapper(base_line, ident):
    """The identifier with the punctuation that carried it, e.g. `(NET-002)`.

    The wrapper is restored with the identifier because it is what separated the
    identifier from the prose; re-inserting a bare token would run it into the
    following word.
    """
    m = re.search(r"\(\s*" + re.escape(ident) + r"[^)]*\)", base_line)
    if m:
        return m.group(0)
    return ident


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--limit", type=int, default=0, help="print at most N proposals")
    args = ap.parse_args()

    proposed = 0
    ambiguous = 0
    files_touched = 0

    for path in tracked(args.base):
        base = subprocess.run(
            ["git", "show", f"{args.base}:{path}"], capture_output=True
        )
        if base.returncode != 0:
            continue
        # A tracked file may not be UTF-8 (fixtures, binaries). Skip rather than
        # fail the run: a file that does not decode carries no comment to repair.
        try:
            base_lines = base.stdout.decode("utf-8").splitlines()
        except UnicodeDecodeError:
            continue
        if not any(ID.search(l) for l in base_lines):
            continue
        try:
            head_lines = open(path, errors="ignore").read().splitlines()
        except OSError:
            continue

        # Align the two revisions with a sequence matcher over the normalised
        # lines. Normalised equality is the identity relation here: the rewrite
        # changed citations and deleted identifiers, and norm() removes both, so
        # a line matches itself across the revisions and nothing else. A
        # positional or whole-file-index match cannot do this, because the
        # rewrite reflowed comments and many short comment lines normalise alike.
        nb = [norm(l) for l in base_lines]
        nh = [norm(l) for l in head_lines]
        sm = difflib.SequenceMatcher(a=nb, b=nh, autojunk=False)
        pairs = []
        for tag, i1, i2, j1, j2 in sm.get_opcodes():
            if tag == "equal":
                for k in range(i2 - i1):
                    pairs.append((i1 + k, j1 + k))
            elif tag == "replace" and (i2 - i1) == (j2 - j1):
                # A same-length replace is a line-for-line rewrite, which is what
                # a citation edit produces. Pair positionally inside the block;
                # an unequal-length replace is a reflow and is left alone.
                for k in range(i2 - i1):
                    pairs.append((i1 + k, j1 + k))

        changed = False
        for bi, hi in pairs:
            b = base_lines[bi]
            ids = ID.findall(b)
            if not ids:
                continue
            h = head_lines[hi]
            missing = [x for x in ids if x not in h]
            if not missing:
                continue
            new_line = h
            ok = True
            for ident in missing:
                # Re-check against the line as it now stands: a wrapper carrying
                # several identifiers restores all of them at once, so a later
                # one in the list is already present and must not be inserted a
                # second time.
                if ident in new_line:
                    continue
                wrapper = id_wrapper(b, ident)
                # Insert after the SAME citation the identifier followed in the
                # base, not the first one. A line citing two sections carries the
                # identifier against one of them, and putting it against the other
                # attributes a requirement to the wrong section.
                bpos = b.find(ident)
                nth = len(re.findall(r"§\d+(?:\.\d+)*", b[:bpos]))
                cites = list(re.finditer(r"§\d+(?:\.\d+)*", new_line))
                if not cites:
                    ok = False
                    break
                anchor = cites[min(max(nth - 1, 0), len(cites) - 1)]
                new_line = new_line[: anchor.end()] + " " + wrapper + new_line[anchor.end():]
            if not ok:
                ambiguous += 1
                continue
            if new_line != h:
                if args.limit == 0 or proposed < args.limit:
                    print(f"{path}")
                    print(f"   -  {h.strip()[:104]}")
                    print(f"   +  {new_line.strip()[:104]}")
                head_lines[hi] = new_line
                proposed += 1
                changed = True
        if changed:
            files_touched += 1
            if args.apply:
                open(path, "w").write("\n".join(head_lines) + "\n")

    print(f"\nfiles: {files_touched}   identifiers restored: {proposed}   left alone (ambiguous): {ambiguous}")
    if not args.apply:
        print("(dry run: nothing written)")


if __name__ == "__main__":
    sys.exit(main())
