#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""List the test functions of each test tier by a static scan.

The scan reads every git-tracked _test.go file outside testdata/ and records
each top-level Test, Fuzz, Example, and Benchmark function. Before the rename
of change 0155 a tier is the numbered directory tests/tierN_<name>; after it,
a tier is tests/<name>. The integration tier counts tests/integration in both
phases, because the rename merged the four files that directory held with the
numbered integration directory. After step S3 the unit tier is the list of
files that unit-moves.tsv names, which tests/tier1_unit held before.

The output is one line per tier with its count, followed by the sorted
function list prefixed by the file's path inside the tier directory (its base
name for the unit tier, whose files S3 spreads across the tree), so two runs compare with diff.

Usage: list_tests.py <repo-root> <before|after> <output-file>
"""
import os
import re
import subprocess
import sys

TIERS = [
    ("static", "tier0_static"),
    ("unit", "tier1_unit"),
    ("component", "tier2_component"),
    ("contract", "tier3_contract"),
    ("integration", "tier4_integration"),
    ("e2e_kind", "tier5_e2e_kind"),
    ("e2e_cloud", "tier6_e2e_cloud"),
    ("load_local", "tier7a_load_local"),
    ("load_kind", "tier7b_load_kind"),
    ("chaos", "tier8_chaos"),
    ("security", "tier9_security"),
    ("conformance", "tier10_conformance"),
    ("docs", "tier11_docs"),
    ("load_cloud", "tier12_load_cloud"),
]

# The static tier is split into these packages by step S2.
STATIC_PARTS = ["build", "chart", "lint", "structure"]

FUNC = re.compile(r"^func ((?:Test|Fuzz|Example|Benchmark)\w*)\(", re.M)
HERE = os.path.dirname(os.path.abspath(__file__))


def tracked(root):
    out = subprocess.run(["git", "ls-files", "*_test.go"], cwd=root,
                         check=True, capture_output=True, text=True).stdout
    return [p for p in out.split("\n") if p and "/testdata/" not in "/" + p]


def unit_targets(root):
    """Return the moved unit test files when S3 has moved them, else None."""
    moves = os.path.join(HERE, "unit-moves.tsv")
    if not os.path.exists(moves):
        return None
    targets = []
    for line in open(moves, encoding="utf-8"):
        line = line.strip()
        if line and not line.startswith("#"):
            targets.append(line.split("\t")[1])
    if all(os.path.exists(os.path.join(root, t)) for t in targets):
        return targets
    return None


def tier_dirs(phase, root):
    """Map each tier to the path prefixes that hold its tests."""
    dirs = {}
    for name, old in TIERS:
        dirs[name] = ["tests/" + (old if phase == "before" else name) + "/"]
    dirs["integration"].append("tests/integration/")
    if phase == "after":
        dirs["unit"] = ["tests/tier1_unit/"]
        moved = unit_targets(root)
        if moved is not None:
            dirs["unit"] = moved
    return dirs


def main():
    root, phase, out = sys.argv[1], sys.argv[2], sys.argv[3]
    files = tracked(root)
    dirs = tier_dirs(phase, root)
    lines = []
    total = 0
    for name, _ in TIERS:
        funcs = set()
        for p in files:
            prefix = next((d for d in dirs[name] if p == d or p.startswith(d)), None)
            if prefix is None:
                continue
            rel = p[len(prefix):] or p.split("/")[-1]
            if name == "unit":
                rel = p.split("/")[-1]
            src = open(os.path.join(root, p), encoding="utf-8").read()
            for f in FUNC.findall(src):
                funcs.add(rel + ":" + f)
        total += len(funcs)
        lines.append("%s\t%d" % (name, len(funcs)))
        lines.extend("  " + f for f in sorted(funcs))
    lines.append("total\t%d" % total)
    with open(out, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
