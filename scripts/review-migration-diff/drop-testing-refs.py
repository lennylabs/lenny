#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Remove references to the testing document from product code.

WHY. Production code should not point a reader at the testing document. A
reference written as a bare `§12.9.8` is worse than a named one, because the
reader takes it for a specification section and looks in the wrong document.

WHY IT IS NOT A SUBSTITUTION. The references take several forms and one rule does
not fit them:

  - a comment whose whole content is the reference, where removing the reference
    leaves an empty comment that should go with it;
  - a reference standing as an aside beside a legitimate specification citation,
    where only the aside goes and the citation stays;
  - a bare section number belonging to the testing document, where the number goes
    and the sentence around it stays;
  - a number declared by BOTH documents, where nothing can be decided from the
    text alone.

Each rule below is applied only where its shape matches, every edit records which
rule produced it, and the ambiguous case is skipped and reported rather than
guessed. The output is reviewed before it is trusted.

Usage:
    drop-testing-refs.py --areas pkg/,cmd/,charts/,migrations/ --out <dir> [--apply]
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import Counter

HEADING = re.compile(r"^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]")
SEC = re.compile(r"§(\d+(?:\.\d+)*(?:\.[a-z])?)")
WS = re.compile(r"[ \t]+")


def declared(paths):
    out = set()
    for f in paths:
        try:
            for line in open(f, errors="ignore"):
                m = HEADING.match(line)
                if m:
                    out.add(m.group(1).rstrip("."))
        except OSError:
            pass
    return out


def comment_body(line):
    """The prose of a comment line, and the prefix that introduces it."""
    m = re.match(r"^(\s*(?://+|#+|--)\s*)(.*)$", line)
    return (m.group(1), m.group(2)) if m else (None, line)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--areas", default="pkg/,cmd/,charts/,migrations/")
    ap.add_argument("--out", required=True)
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    areas = tuple(a for a in args.areas.split(",") if a)

    ls = lambda p: subprocess.run(["git", "ls-files", p], capture_output=True, text=True).stdout.split()
    spec = declared(ls("spec/*.md"))
    for n in list(spec):
        spec.add(n.split(".")[0])
    testing = declared(["TESTING.md"])
    testing_only = testing - spec
    both = testing & spec

    edits, skipped = [], []
    for f in subprocess.run(["git", "ls-files"], capture_output=True, text=True).stdout.split():
        if not f.startswith(areas) or f.endswith(".md"):
            continue
        try:
            lines = open(f, errors="ignore").read().splitlines(keepends=True)
        except OSError:
            continue
        changed = False
        out = []
        for i, raw in enumerate(lines, 1):
            line = raw.rstrip("\n")
            has_name = "TESTING.md" in line
            nums = [n for n in SEC.findall(line)]
            t_nums = [n for n in nums if n in testing_only]
            amb = [n for n in nums if n in both]
            if not has_name and not t_nums:
                out.append(raw)
                continue
            if amb and not has_name:
                skipped.append({"file": f, "line": i, "text": line.strip()[:150],
                                "why": f"§{amb[0]} is declared by both documents"})
                out.append(raw)
                continue

            new = line
            rule = None
            # R4: the document and its section written together.
            new2 = re.sub(r"\(?\bTESTING\.md\b\)?[ \t]*(§\d+(?:\.\d+)*)", lambda m: "", new)
            if new2 != new and not SEC.search(new2):
                new, rule = new2, "R4 name+section"
            # R2: the document named as an aside beside other prose.
            if rule is None and has_name:
                new = re.sub(r"[ \t]*\(\s*TESTING\.md\s*\)", "", new)
                new = re.sub(r"[ \t]*\bTESTING\.md\b[ \t]*", " ", new)
                rule = "R2 named aside"
            # R3: a bare section number belonging to the testing document only.
            for n in t_nums:
                new = re.sub(r"§" + re.escape(n) + r"(?![\d.])[ \t]*", "", new)
                rule = rule or "R3 bare section"

            prefix, body = comment_body(new)
            # R1: the reference was the whole comment; the comment goes with it.
            if prefix is not None and not re.sub(r"[\s./,;:—-]", "", body):
                rule = "R1 empty comment"
                changed = True
                edits.append({"file": f, "line": i, "rule": rule,
                              "before": line.strip()[:160], "after": "(line removed)"})
                continue

            new = WS.sub(" ", new).rstrip()
            new = re.sub(r"\(\s*\)", "", new)
            new = re.sub(r"\s+([.,;:)])", r"\1", new)

            # A removal is clean only if what closes over the gap reads as one
            # sentence. Checking the start of the comment is not enough: most of
            # these references sit mid-line, and removing one leaves a separator
            # joined to a separator, an unmatched bracket, a preposition with no
            # object, or a possessive with no owner. Each of those was produced
            # by an earlier run of this tool and each needs an author, not a
            # regex, so the site is deferred rather than tidied.
            _, body_after = comment_body(new)
            bad = (
                re.search(r"[/,;&]\s*[/,;)]", new)
                or re.search(r"(^|\s)[/,;]\s*$", new.rstrip())
                or re.search(r"\s[/,;]\s", new) and not re.search(r"\S\s*[/,;]\s*\S", new)
                or re.search(r"\b(by|to|in|of|for|with|per|owns|naming)\s*[.,;)]", new)
                or re.search(r"\b(by|to|of|for)\s+and\b", new)
                or re.search(r"(^|[\s(])'s\b", new)
                or new.count("(") != new.count(")")
                or re.search(r"[\s(]\.[a-z]\b", new)
                or re.match(r"^(and|or|with|per|see|is|owns)\b", body_after.strip(), re.I)
                or re.match(r"^[—\-/,;:.]", body_after.strip())
                or re.match(r"^spec:\s*([—\-/,;:]|$)", body_after.strip(), re.I)
            )
            if bad:
                skipped.append({"file": f, "line": i, "text": line.strip()[:150],
                                "why": "removal leaves a fragment; needs hand correction"})
                out.append(raw)
                continue

            if new.rstrip() != line.rstrip():
                changed = True
                edits.append({"file": f, "line": i, "rule": rule,
                              "before": line.strip()[:160], "after": new.strip()[:160]})
            out.append(new + "\n")
        if changed and args.apply:
            open(f, "w").write("".join(out))

    with open(os.path.join(args.out, "edits.json"), "w") as fh:
        json.dump({"edits": edits, "skipped": skipped}, fh, indent=1)
    print(f"edits: {len(edits)}   skipped as ambiguous: {len(skipped)}")
    print("  by rule:", dict(Counter(e["rule"] for e in edits)))
    for e in edits[:10]:
        print(f"\n  {e['file']}:{e['line']}  [{e['rule']}]\n    -  {e['before'][:104]}\n    +  {e['after'][:104]}")
    if not args.apply:
        print("\n(dry run: nothing written)")


if __name__ == "__main__":
    sys.exit(main())
