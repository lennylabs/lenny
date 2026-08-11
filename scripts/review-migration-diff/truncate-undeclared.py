#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Reduce a citation that over-specifies to the deepest section that exists.

WHY THESE ARE WRONG. `§15.0`, `§27.5.4` and `§25.6.1` name sections no document
declares. A reader following one finds nothing. In every case the citation names a
real section and then one component too many: §15 carries 15.1 through 15.7 and no
15.0, §27.5 exists and has no fourth child, and §25 has no sub-subsections at all.

WHY TRUNCATION IS THE RIGHT REPAIR. The trailing component is the only part that
does not resolve, so dropping it yields the section the citation was reaching into.
That broadens the pointer by one level and never moves it: §27.5.4 and §27.5 are
the same subject at different resolution. The alternative, choosing which existing
child was meant, is a guess, and a citation that points confidently at the wrong
child is worse than one that points at the parent.

WHAT THIS PASS WILL NOT DO. It never invents a section and never deepens one. A
citation with no declared ancestor at all is left alone and reported: nothing in
the number says where it should have pointed, so it needs a reader.

EXTERNAL STANDARDS ARE NOT IN SCOPE. `45 CFR §164.312` and `RFC 7232 §2.3` are
correct as written and name documents outside this repository. They are excluded
by the caller, and this pass additionally refuses any number whose top-level
component is not a section of the specification.

Usage:
    truncate-undeclared.py --sites <phase3/sites.json> --out <dir> [--apply]
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import Counter

HEADING = re.compile(r"^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]")

# A line citing one of these names a document outside this repository, whose
# section numbering is not the specification's and must not be rewritten.
EXTERNAL = re.compile(
    r"\b(RFC\s*\d+|CFR|ISO\s*\d+|NIST|SP\s*800|OAuth|JSON[- ]RPC|SemVer|OCSF"
    r"|OpenSLO|CloudEvents|W3C|IETF|HIPAA|SOC\s*2|PCI[- ]DSS)\b", re.I)

# A §-number written after a markdown link belongs to the linked page.
DOC_RELATIVE = re.compile(r"\]\([^)]*\)[^§]{0,40}§")


def declared_sections():
    """Every section number the specification declares as a heading."""
    files = subprocess.run(["git", "ls-files", "spec/*.md"],
                           capture_output=True, text=True).stdout.split()
    out = set()
    for f in files:
        for line in open(f, errors="ignore"):
            m = HEADING.match(line)
            if m:
                out.add(m.group(1).rstrip("."))
    return out


def longest_declared_prefix(num, declared):
    """The deepest ancestor of `num` that is a declared section, or None."""
    parts = num.split(".")
    for k in range(len(parts) - 1, 0, -1):
        cand = ".".join(parts[:k])
        if cand in declared:
            return cand
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sites", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    declared = declared_sections()
    sites = json.load(open(args.sites))

    edits, unresolvable = [], []
    for s in sites:
        num = s["section"]
        if num in declared:
            continue  # already resolves; nothing to do
        # A zero-padded component is a spelling of a declared section rather than
        # a different one: §02 and §08.3 are §2 and §8.3 written with a leading
        # zero, and no heading is ever padded. Unpad before looking further.
        unpadded = ".".join(str(int(p)) if p.isdigit() else p for p in num.split("."))
        target = unpadded if unpadded in declared else longest_declared_prefix(unpadded, declared)
        if target is None:
            unresolvable.append(s)
            continue
        edits.append({**s, "to": target})

    # Rewrite every occurrence in the file rather than the one recorded line. The
    # recorded numbers come from an earlier scan and drift as other passes edit
    # the file, so a line-addressed rewrite silently misses sites. Occurrence
    # addressing cannot drift, and it is safe here precisely because the number
    # being replaced resolves nowhere: every occurrence of it is equally wrong, so
    # there is no correct instance for a file-wide pass to damage.
    by_file = {}
    for e in edits:
        by_file.setdefault(e["file"], {})[e["section"]] = e["to"]

    applied, per_pair, held = 0, Counter(), []
    for f, mapping in sorted(by_file.items()):
        try:
            text = open(f, errors="ignore").read()
        except OSError:
            continue
        # A file that cites an external standard once carries that standard's
        # numbering throughout: a test headed "RFC 8693 §2.2.1" goes on to write
        # "§2.2.1 response envelope" with no repeat of the standard's name. The
        # guard therefore withdraws a number from the whole file when any line
        # citing it also names an external document, rather than judging each
        # line alone.
        external_here = {n for n in mapping
                         if any(EXTERNAL.search(l) for l in text.splitlines()
                                if re.search(r"§0*" + re.escape(n) + r"(?!\.?\d)", l))}
        for n in external_here:
            held.append({"file": f, "text": f"§{n} withdrawn: the file cites an external standard"})
        mapping = {n: t for n, t in mapping.items() if n not in external_here}
        if not mapping:
            continue

        # Match only the numbers this file actually needs rewritten, so a line is
        # judged by the guards only when it carries one of them. Testing every
        # line would hold back a target that merely shares a line with the word
        # "OAuth", and would report skips for lines that were never in scope.
        carries = re.compile("|".join(r"§0*" + re.escape(n) + r"(?!\.?\d)"
                                      for n in sorted(mapping, key=len, reverse=True)))
        out = []
        for raw in text.splitlines(keepends=True):
            line = raw
            if not carries.search(line):
                out.append(raw)
                continue
            # Two kinds of §-number on a line are correct and must survive. A
            # citation of an external standard names a document this repository
            # does not contain, so its sections are not ours to resolve. A number
            # following a markdown link is a section of the linked page, and
            # truncating it would repoint a reader inside a document that does
            # declare it. Neither is visible from the number alone, so both are
            # checked here, per line, rather than when the site list was built.
            if EXTERNAL.search(line) or DOC_RELATIVE.search(line):
                held.append({"file": f, "text": line.strip()[:130]})
                out.append(raw)
                continue
            # Deepest number first, so rewriting §27.5 never eats the prefix of a
            # §27.5.4 that has not been handled yet.
            for num in sorted(mapping, key=lambda n: -len(n)):
                # The boundary guard keeps a longer number that merely starts the
                # same way intact: without it, replacing §15.0 would corrupt
                # §15.0.2. It refuses a following digit, and a dot only when a
                # digit follows it, so a citation ending a sentence still matches.
                pat = re.compile(r"§0*" + re.escape(num) + r"(?!\.?\d)")
                line, n = pat.subn("§" + mapping[num], line)
                if n:
                    per_pair[(num, mapping[num])] += n
                    applied += n
            out.append(line)
        new = "".join(out)
        if args.apply and new != text:
            open(f, "w").write(new)

    json.dump({"edits": edits, "unresolvable": unresolvable, "held": held,
               "counts": {f"{a} -> {b}": n for (a, b), n in per_pair.items()}},
              open(os.path.join(args.out, "truncations.json"), "w"), indent=1)

    print(f"citations resolved to a declared section: {applied}")
    for (a, b), n in per_pair.most_common(14):
        print(f"   §{a:<10} -> §{b:<8} {n:4d} occurrences")
    if held:
        print(f"\nheld back, the number is not the specification's: {len(held)}")
        for h in held[:8]:
            print(f"   {h['file']}  {h['text'][:96]}")
    if unresolvable:
        print(f"\nno declared ancestor, left alone for a reader: {len(unresolvable)}")
        for u in unresolvable[:12]:
            print(f"   {u['file']}:{u['line']}  §{u['section']}  {u['text'][:80]}")
    if not args.apply:
        print("\n(dry run: nothing written)")


if __name__ == "__main__":
    sys.exit(main())
