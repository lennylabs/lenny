#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Classify every §-citation in the tree by which document declares its number.

WHY. A tree-wide scan reports ~1,676 citations of a section the specification does
not declare, which reads as one large stale population. It is not one population.
Sampling shows at least three, with different causes and different fixes:

  - a number that belongs to TESTING.md, written bare as `§12.3.7` so a reader
    takes it for a specification section;
  - a number that belongs to a PROPOSAL's own headings, so `§3.4` sends a reader
    to spec/03, which carries no subsections at all, when the rule it names is
    `### 3.4 The recycle disposition` in proposals/0002;
  - a number no document declares.

Counting them together hides that the second class is a different defect: a
proposal-internal identifier standing in product code, which the project's
conventions forbid outright.

WHAT IT EMITS.

  citations.json  every unresolved citation, bucketed, with file, line and the
                  citing text, so each bucket can be worked as a unit.
  testing-refs.json  every reference to TESTING.md from non-documentation files,
                  whether written as a bare section number or as the filename.
                  The intent is that product code should not cite the testing
                  document at all; this is the inventory that decision needs, and
                  nothing acts on it yet.

Ambiguity is reported rather than resolved: a number declared by more than one
document is listed under `ambiguous` with every declarer named, because choosing
for it would be a guess dressed as a classification.

Usage:
    classify-citations.py --out <dir>
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import Counter, defaultdict

SEC = re.compile(r"§(\d+(?:\.\d+)*)")
HEADING = re.compile(r"^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]")
TESTING_NAME = re.compile(r"\bTESTING\.md\b")

# Files whose citations are historical record rather than live pointers: the
# audit logs, the queue, and the frozen reference documents.
RECORD_FILES = {
    "BUILD-GAPS.md", "BUILD-PLAN.md", "BUILD-PROGRESS.md", "TEST-GAPS.md",
    "PROPOSAL-QUEUE.md", "CHANGELOG.md", "ROADMAP.md",
    "gateway-runtime-comms.md", "gateway-runtime-comms-remediation.md",
}
# The migration's own fixtures and drivers cite by construction.
SKIP_PREFIX = ("proposals/", "scripts/specshift/", "tests/tier0_static/testdata/", ".claude/")

# What counts as product code for the TESTING.md inventory: everything that is
# not documentation, not the testing document itself, and not an audit record.
CODE_PREFIX = ("pkg/", "cmd/", "tests/", "charts/", "migrations/", "sdks/", "scripts/", "build/", ".github/")


def declared_numbers(paths):
    out = defaultdict(set)
    for f in paths:
        try:
            for line in open(f, errors="ignore"):
                m = HEADING.match(line)
                if m:
                    out[m.group(1).rstrip(".")].add(f)
        except OSError:
            pass
    return out


def ls(pattern):
    return subprocess.run(["git", "ls-files", pattern], capture_output=True, text=True).stdout.split()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    spec = declared_numbers(ls("spec/*.md"))
    # A file-level number licenses citing the file: "28" for spec/28.
    for n in list(spec):
        spec[n.split(".")[0]].update(spec[n])
    proposals = declared_numbers(ls("proposals/*.md"))
    testing = declared_numbers(["TESTING.md"])

    buckets = defaultdict(list)
    testing_refs = []

    for f in subprocess.run(["git", "ls-files"], capture_output=True, text=True).stdout.split():
        if f.startswith(SKIP_PREFIX) or os.path.basename(f) in RECORD_FILES or f == "TESTING.md":
            continue
        try:
            text = open(f, errors="ignore").read()
        except OSError:
            continue
        is_code = f.startswith(CODE_PREFIX) and not f.endswith(".md")
        for i, line in enumerate(text.splitlines(), 1):
            # The TESTING.md inventory: one record per SITE, not per mention. A
            # line naming the document and its section is one reference, and
            # counting both forms doubles it.
            named_here = bool(is_code and TESTING_NAME.search(line))
            if named_here:
                testing_refs.append({"file": f, "line": i, "form": "qualified",
                                     "text": line.strip()[:160]})
            for m in SEC.finditer(line):
                n = m.group(1)
                if n in spec:
                    continue
                where = []
                if n in testing:
                    where.append("TESTING.md")
                if n in proposals:
                    where.append("proposals")
                rec = {"file": f, "line": i, "section": n, "text": line.strip()[:160]}
                if len(where) > 1:
                    buckets["ambiguous"].append({**rec, "declared_by": where})
                elif where == ["TESTING.md"]:
                    buckets["testing-numbering"].append(rec)
                    if is_code and not named_here:
                        # A bare number the reader will take for a specification
                        # section: both a reference to remove and a citation that
                        # currently misleads.
                        testing_refs.append({"file": f, "line": i, "form": "bare-section",
                                             "section": n, "text": line.strip()[:160]})
                elif where == ["proposals"]:
                    buckets["proposal-numbering"].append(
                        {**rec, "declared_by": sorted(proposals[n])[:3]})
                else:
                    buckets["declared-nowhere"].append(rec)

    with open(os.path.join(args.out, "citations.json"), "w") as fh:
        json.dump(buckets, fh, indent=1)
    with open(os.path.join(args.out, "testing-refs.json"), "w") as fh:
        json.dump(testing_refs, fh, indent=1)

    print("unresolved §-citations, by the document that declares the number:\n")
    for b in ("testing-numbering", "proposal-numbering", "declared-nowhere", "ambiguous"):
        rows = buckets.get(b, [])
        print(f"  {len(rows):5d}  {b}")
        by_sec = Counter(r["section"] for r in rows)
        for s, c in by_sec.most_common(4):
            first = next(r for r in rows if r["section"] == s)
            print(f"           §{s:<9} {c:4d} sites   {first['file']}:{first['line']}")

    print(f"\nTESTING.md referenced from product code: {len(testing_refs)} site(s)")
    print("  by form:", dict(Counter(r["form"] for r in testing_refs)))
    print("  by area:", dict(Counter(r["file"].split("/")[0] for r in testing_refs)))
    print(f"\n  -> {os.path.join(args.out,'citations.json')}")
    print(f"  -> {os.path.join(args.out,'testing-refs.json')}")


if __name__ == "__main__":
    sys.exit(main())
