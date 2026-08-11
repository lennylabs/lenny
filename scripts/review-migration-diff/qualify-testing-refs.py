#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Name the document a bare testing-model section number belongs to.

WHAT IS WRONG. A line reading `the §12.9.8 egress-capture annotation` names a
section the specification does not declare. TESTING.md declares it. A reader
sees the §-form, looks in spec/, finds nothing, and concludes the reference is
stale. It is not stale; it points at a document the reader was not told about.

WHY QUALIFY RATHER THAN REMOVE. Earlier passes over this population tried to
delete these references from product code. Deletion is right where the pointer
was noise, and that pass has already run over pkg/, cmd/, charts/ and
migrations/. What remains is mostly tests, workflows and registers, where the
reference is load-bearing: a test of the credential-leakage battery genuinely
traces to the battery's definition, and a CI workflow implementing the flake
budget genuinely implements it. Removing the pointer there would lose the trace.

WHY THIS IS SAFE WHERE REWRITING WAS NOT. The edit only inserts a document name
in front of a number it already agrees with. It removes nothing, reorders
nothing, and cannot strand a preposition or a bracket, which is what every
removal pass had to be screened for. The check afterwards is that the line is
unchanged except for the inserted name.

WHAT IS OUT OF SCOPE. A number the specification also declares is ambiguous from
the text alone and is left for a reader. TESTING.md's own prose is left alone,
since a document does not cite itself by name. The review tooling under
scripts/review-migration-diff/ quotes these forms to describe them and is
excluded, or it would rewrite its own explanations.

Usage:
    qualify-testing-refs.py --out <dir> [--apply]
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import Counter

HEADING = re.compile(r"^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]")

# The tail of `[§13.0](../TESTING.md#...)` as seen from just after the number.
LINKED_TO_TESTING = re.compile(r"[^\]]*\]\([^)]*TESTING\.md")

# Paths whose §-numbers describe the problem rather than commit it, or record a
# past state that is not ours to edit. The ledgers and proposals say what was
# true when they were written; rewriting a reference inside one changes the
# record rather than fixing a pointer. The style rules quote the bare form as the
# example of what to avoid, and the review tooling quotes it to explain itself.
EXCLUDED = (
    "TESTING.md",
    "scripts/review-migration-diff/",
    ".claude/",
    "BUILD-GAPS.md",
    "BUILD-PROGRESS.md",
    "TEST-GAPS.md",
    "PROPOSAL-QUEUE.md",
    "proposals/",
    "dist/",
    "spec/",
)


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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    ls = lambda p: subprocess.run(["git", "ls-files", p], capture_output=True,
                                  text=True).stdout.split()
    spec = declared(ls("spec/*.md"))
    spec |= {n.split(".")[0] for n in spec}
    testing = declared(["TESTING.md"])
    # Only a number TESTING.md declares and the specification does not can be
    # attributed from the number alone.
    only_testing = testing - spec
    if not only_testing:
        print("no number is declared by TESTING.md alone; nothing to do")
        return

    pat = re.compile(
        r"(?<!\w)§(" + "|".join(sorted((re.escape(n) for n in only_testing),
                                       key=len, reverse=True)) + r")(?!\.?\d)")

    edits, ambiguous = [], []
    for f in subprocess.run(["git", "ls-files"], capture_output=True,
                            text=True).stdout.split():
        if f.startswith(EXCLUDED):
            continue
        try:
            text = open(f, errors="ignore").read()
        except OSError:
            continue
        if "§" not in text:
            continue
        out, changed = [], False
        for raw in text.splitlines(keepends=True):
            def qualify(m):
                # A reference already naming the document, anywhere earlier on
                # the line, is qualified: leave it rather than write the name
                # twice.
                if "TESTING.md" in raw[: m.start()]:
                    return m.group(0)
                # `[§13.0](../TESTING.md#130-...)` names the document in the link
                # target, so a reader following it arrives where the section is
                # declared. Prefixing the name would only duplicate it in the
                # link's visible text.
                if LINKED_TO_TESTING.match(raw[m.end():]):
                    return m.group(0)
                return "TESTING.md " + m.group(0)

            new = pat.sub(qualify, raw)
            if new != raw:
                changed = True
                edits.append({"file": f, "before": raw.strip()[:150],
                              "after": new.strip()[:150]})
            out.append(new)
        if changed and args.apply:
            open(f, "w").write("".join(out))

    json.dump({"edits": edits, "ambiguous": ambiguous},
              open(os.path.join(args.out, "qualifications.json"), "w"), indent=1)
    print(f"bare references qualified: {len(edits)}")
    by_area = Counter(e["file"].split("/")[0] for e in edits)
    print("  by area:", dict(by_area))
    print(f"  numbers TESTING.md declares alone: {len(only_testing)}")
    for e in edits[:8]:
        print(f"\n  {e['file']}\n    -  {e['before'][:104]}\n    +  {e['after'][:104]}")
    if not args.apply:
        print("\n(dry run: nothing written)")


if __name__ == "__main__":
    sys.exit(main())
