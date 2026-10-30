#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Split the migration diff into what a machine can decide and what needs a reader.

WHY A FUNNEL. The branch diff carries 14,642 hunks and 17,446 paired lines. Sending
all of them to a reviewer, human or model, spends the budget on the 73% that differ
only by a citation token and are decidable without judgement, and buries the
quarter that are not. Worse, it asks the question at the wrong level: every defect
this migration produced was a defect of a transformation SHAPE, applied uniformly,
not a defect of scattered individual sites.

WHAT COMES OUT.

  shapes.json    Every distinct before-to-after citation transformation among the
                 lines that differ only by a citation, with its count and three
                 example sites. 327 shapes cover 12,728 sites; 45 cover 95% of
                 them. Reviewing one exemplar per shape decides every site sharing
                 it, and it asks the question where the defect lives.

  batches/*.json The lines where something other than a citation changed. These are
                 the ones no mechanical rule decides, so they go to a reader.

A line that differs only by a citation is NOT thereby correct: the shape may be
wrong, which is what shapes.json is for. What the split buys is that the judgement
is made once per shape rather than once per site.

Usage:
    funnel.py --base <rev> --out <dir> [--batch-size 25]
"""

import argparse
import difflib
import hashlib
import json
import os
import re
import subprocess
import sys
from collections import defaultdict

# Every token the migration rewrites: a section citation in any spelling, a
# file-qualified markdown target, and a bare fragment.
CITE = re.compile(
    r"§\d+(?:\.\d+)*(?:\s*(?:lines?|—|-)?\s*[0-9][0-9,\-–/\s]*)?"
    r"|\b[0-9]{2}_[a-z-]+\.md(?:#[a-z0-9-]+)?"
    r"|(?<![A-Za-z0-9])#[a-z0-9-]{4,}"
)
WS = re.compile(r"\s+")
NUM = re.compile(r"\d+")


def strip_citations(line):
    return WS.sub(" ", CITE.sub("", line)).strip()


def shape_of(before, after):
    """The transformation abstracted over section numbers.

    Two sites share a shape when the same kind of citation became the same kind
    of citation. The section number is abstracted away because a rule that is
    wrong for `§4.7 line N (x)` is wrong for `§13.2 line N (x)` too.
    """
    ta = tuple(NUM.sub("N", t) for t in CITE.findall(before))
    tb = tuple(NUM.sub("N", t) for t in CITE.findall(after))
    return json.dumps([ta, tb])


def parse_diff(base):
    """Aligned -/+ line pairs, per hunk, with the file each came from.

    Pairing must be an ALIGNMENT, not adjacency. A -U0 hunk whose - and + counts
    differ is a reflow, and pairing its lines by position marries unrelated ones:
    a calibration run over adjacency-paired input spent most of its findings on
    pairs the pairing itself had invented. Aligning each hunk with a sequence
    matcher over citation-stripped lines gives each removed line the added line
    it actually became, and leaves genuinely added or removed lines unpaired.
    """
    out = subprocess.run(
        ["git", "diff", "-U0", base, "HEAD"], capture_output=True, text=True
    ).stdout.splitlines()
    path = None
    pairs, singles = [], []
    minus, plus = [], []

    def flush():
        if not minus and not plus:
            return
        sm = difflib.SequenceMatcher(
            a=[strip_citations(x) for x in minus],
            b=[strip_citations(x) for x in plus],
            autojunk=False,
        )
        used_a, used_b = set(), set()
        for tag, i1, i2, j1, j2 in sm.get_opcodes():
            if tag == "equal" or (tag == "replace" and (i2 - i1) == (j2 - j1)):
                for k in range(i2 - i1):
                    pairs.append((path, minus[i1 + k], plus[j1 + k]))
                    used_a.add(i1 + k)
                    used_b.add(j1 + k)
        for i, l in enumerate(minus):
            if i not in used_a:
                singles.append((path, l, None))
        for j, l in enumerate(plus):
            if j not in used_b:
                singles.append((path, None, l))
        minus.clear()
        plus.clear()

    for l in out:
        if l.startswith("+++ b/"):
            flush()
            path = l[6:]
            continue
        if l.startswith("@@"):
            flush()
            continue
        if l.startswith("-") and not l.startswith("---"):
            minus.append(l[1:])
        elif l.startswith("+") and not l.startswith("+++"):
            plus.append(l[1:])
        else:
            flush()
    flush()
    return pairs, singles


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--batch-size", type=int, default=25)
    args = ap.parse_args()

    os.makedirs(os.path.join(args.out, "batches"), exist_ok=True)
    pairs, singles = parse_diff(args.base)

    shapes = defaultdict(lambda: {"count": 0, "examples": []})
    mixed = []
    for path, before, after in pairs:
        if strip_citations(before) == strip_citations(after):
            s = shapes[shape_of(before, after)]
            s["count"] += 1
            if len(s["examples"]) < 3:
                s["examples"].append({"file": path, "before": before, "after": after})
        else:
            mixed.append({"file": path, "before": before, "after": after})

    ordered = sorted(shapes.items(), key=lambda kv: -kv[1]["count"])
    with open(os.path.join(args.out, "shapes.json"), "w") as f:
        json.dump(
            [
                {"shape": json.loads(k), "count": v["count"], "examples": v["examples"]}
                for k, v in ordered
            ],
            f,
            indent=1,
        )

    # Batch the mixed lines. The id is a content hash, so a re-run reproduces the
    # same batches and a completed batch can be skipped rather than repeated.
    n = 0
    for i in range(0, len(mixed), args.batch_size):
        chunk = mixed[i : i + args.batch_size]
        bid = hashlib.sha256(
            json.dumps(chunk, sort_keys=True).encode()
        ).hexdigest()[:16]
        with open(os.path.join(args.out, "batches", f"{bid}.json"), "w") as f:
            json.dump({"id": bid, "kind": "mixed", "items": chunk}, f, indent=1)
        n += 1

    # One batch per shape-exemplar group, so the shapes are reviewed by the same
    # driver rather than by a second mechanism.
    for i in range(0, len(ordered), args.batch_size):
        chunk = [
            {"file": v["examples"][0]["file"] if v["examples"] else "",
             "before": v["examples"][0]["before"] if v["examples"] else "",
             "after": v["examples"][0]["after"] if v["examples"] else "",
             "applies_to_sites": v["count"]}
            for _, v in ordered[i : i + args.batch_size]
        ]
        bid = hashlib.sha256(json.dumps(chunk, sort_keys=True).encode()).hexdigest()[:16]
        with open(os.path.join(args.out, "batches", f"{bid}.json"), "w") as f:
            json.dump({"id": bid, "kind": "shape", "items": chunk}, f, indent=1)
        n += 1

    print(f"paired lines:            {len(pairs)}")
    print(f"  citation-only:         {sum(v['count'] for v in shapes.values())}")
    print(f"    distinct shapes:     {len(shapes)}")
    print(f"  mixed (need a reader): {len(mixed)}")
    print(f"unpaired lines (reflow/new content, not batched): {len(singles)}")
    print(f"\nbatches written: {n}  ->  {os.path.join(args.out,'batches')}")


if __name__ == "__main__":
    sys.exit(main())
