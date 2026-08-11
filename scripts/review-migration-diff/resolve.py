#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Resolve every citation and link the migration wrote, against the tree it wrote them for.

WHY THIS IS SEPARATE FROM THE DIFF REVIEW. A reviewer reading a hunk cannot tell
whether the citation in it resolves: the hunk shows the pointer, never the target.
Two of the defect classes this migration produced were invisible for exactly that
reason — a pointer repointed to a section that does not carry the material, and a
link whose target heading no longer exists. Both look correct in isolation.

WHAT IT CHECKS, over every line the diff added:

  1. Every §X.Y names a heading that exists now. A citation of a section the tree
     does not declare resolves nowhere, whoever wrote it.
  2. Every intra-repo markdown link resolves: the file exists and the fragment
     matches a heading slug or an explicit anchor attribute on that page.
  3. Every link whose visible label names a section number agrees with the section
     its target resolves to. A label saying one section and a target going to
     another misleads a reader who trusts the label, and neither half is wrong on
     its own, so only this cross-check finds it.

It reports; it changes nothing.

Usage:
    resolve.py --base <rev>
"""

import argparse
import os
import re
import subprocess
import sys

SEC = re.compile(r"§(\d+(?:\.\d+)*)")
LINK = re.compile(r"\[([^\]]{0,200})\]\(([^)\s]+)\)")
HEADING = re.compile(r"^(#{1,6})\s+(.*?)\s*$")
ANCHOR_ATTR = re.compile(r"\{:\s*#([a-z0-9-]+)\s*\}")
LABEL_SEC = re.compile(r"(?:§|[Ss]ection\s*)(\d+(?:\.\d+)*)")


def slug(title):
    """GitHub-style heading slug: lowercase, punctuation dropped, spaces hyphenated."""
    s = title.lower()
    s = re.sub(r"`|\*|_", "", s)
    s = re.sub(r"[^\w\s-]", "", s)
    # Each whitespace character becomes one hyphen, not each run of them.
    # Removing a punctuation mark between two spaces leaves two spaces, and the
    # anchor a renderer produces carries two hyphens there; collapsing the run
    # reports every such heading as unreachable when the link is in fact correct.
    return re.sub(r"\s", "-", s).strip("-")


def index_tree():
    """Section numbers the tree declares, and the anchors each markdown page offers."""
    sections, anchors = set(), {}
    files = subprocess.run(["git", "ls-files", "*.md"], capture_output=True, text=True).stdout.split()
    for f in files:
        try:
            text = open(f, errors="ignore").read()
        except OSError:
            continue
        page = set()
        for line in text.splitlines():
            m = HEADING.match(line)
            if m:
                title = m.group(2)
                page.add(slug(title))
                # A spec heading numbers itself as "28. Communication Channels"
                # at file level and "28.5 Contract cards" below it, so the
                # separator after the number is a dot or a space. Matching only
                # the space form makes every whole-file citation look unresolved.
                n = re.match(r"(\d+(?:\.\d+)*)[.\s]", title)
                if n and f.startswith("spec/"):
                    num = n.group(1).rstrip(".")
                    sections.add(num)
                    # a file-level number also licenses citing the file itself
                    sections.add(num.split(".")[0])
            for a in ANCHOR_ATTR.finditer(line):
                page.add(a.group(1))
        anchors[f] = page
    return sections, anchors


def added_lines(base):
    out = subprocess.run(["git", "diff", "-U0", base, "HEAD"], capture_output=True, text=True).stdout
    path = None
    for line in out.splitlines():
        if line.startswith("+++ b/"):
            path = line[6:]
        elif line.startswith("+") and not line.startswith("+++"):
            yield path, line[1:]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    args = ap.parse_args()

    sections, anchors = index_tree()
    bad_sec, bad_link, bad_label = [], [], []
    seen = set()

    for path, line in added_lines(args.base):
        # Exclude what no pass writes and what is not prose: the proposals, the
        # migration tooling's own fixtures, and the read-excluded root records.
        if not path or path.startswith(
            ("proposals/", "scripts/specshift/", "tests/tier0_static/testdata/", ".claude/")
        ) or os.path.basename(path) in {
            "BUILD-GAPS.md", "BUILD-PLAN.md", "BUILD-PROGRESS.md", "TEST-GAPS.md",
            "PROPOSAL-QUEUE.md", "gateway-runtime-comms.md",
            "gateway-runtime-comms-remediation.md", "TESTING.md",
        }:
            continue
        for m in SEC.finditer(line):
            num = m.group(1)
            key = (path, num)
            if num in sections or key in seen:
                continue
            seen.add(key)
            bad_sec.append((path, num, line.strip()[:110]))
        if not path.endswith(".md"):
            continue
        for m in LINK.finditer(line):
            label, target = m.group(1), m.group(2)
            if "://" in target or target.startswith("mailto:"):
                continue
            file_part, _, frag = target.partition("#")
            tgt = os.path.normpath(os.path.join(os.path.dirname(path), file_part)) if file_part else path
            if file_part and not os.path.exists(tgt):
                # The documentation site resolves extensionless and .html links
                # itself, so a target that exists as .md is resolved, not broken.
                if os.path.exists(tgt + ".md"):
                    tgt = tgt + ".md"
                elif tgt.endswith(".html") and os.path.exists(tgt[:-5] + ".md"):
                    tgt = tgt[:-5] + ".md"
                else:
                    bad_link.append((path, target, "target file does not exist"))
                    continue
            if frag and frag not in anchors.get(tgt, set()):
                bad_link.append((path, target, "fragment matches no heading or anchor"))
                continue
            # the label-versus-target cross-check
            lm = LABEL_SEC.search(label)
            if lm and frag:
                fm = re.match(r"(\d+)(\d)?(\d)?-", frag)
                digits = re.match(r"^(\d+)-", frag)
                if digits:
                    d = digits.group(1)
                    dotted = ".".join([d[:2].lstrip("0") or d[:2]] + list(d[2:])) if len(d) > 2 else d
                    if lm.group(1).replace(".", "") != d:
                        bad_label.append((path, label[:44], target, lm.group(1)))

    print(f"citations naming a section the tree does not declare: {len(bad_sec)}")
    for p, n, l in bad_sec[:15]:
        print(f"   §{n}  {p}\n      {l}")
    print(f"\nmarkdown links that do not resolve: {len(bad_link)}")
    for p, t, why in bad_link[:15]:
        print(f"   {p} -> {t}   ({why})")
    print(f"\nlinks whose label names a different section from the target: {len(bad_label)}")
    for p, lab, t, n in bad_label[:15]:
        print(f"   {p}: label says §{n} -> {t}\n      label: {lab}")


if __name__ == "__main__":
    sys.exit(main())
