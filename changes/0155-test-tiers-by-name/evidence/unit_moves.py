#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Move the tests of tests/tier1_unit beside the code they test.

This is the mechanical part of change 0155, step S3. It runs from the root
of a clean checkout and reads unit-moves.tsv, which lists each test file and
the file it moves to. Every test keeps its name and its annotations.

- A whole file moves with `git mv` and takes the package of its new
  directory as an external test package (`<package>_test`), so its helpers
  cannot collide with the package's own tests. A file moved into a main
  package takes `main_test`.
- concurrency_test.go tests four packages. Each of its tests moves into a
  new file beside the package it tests.
- The doc.go of a test-only package goes, and its package comment, which
  records why the tests exist, moves into the test file as a file comment.
- tests/tier1_unit/README.md goes with the directory.
- Every path reference to a moved file outside the historical records is
  rewritten.

Usage: unit_moves.py
"""
import os
import re
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = "tests/tier1_unit"
HISTORICAL = (
    "changes/", "proposals/", "BUILD-GAPS.md", "TEST-GAPS.md", "CODE-REVIEW.md",
    "CHANGELOG.md", "gateway-runtime-comms", "PROPOSAL-QUEUE.md",
)

# The package clause each moved file takes.
PACKAGES = {
    "cmd/lenny-gateway": "main_test",
    "pkg/chart/values": "values_test",
    "tests/testinfra/lifecycleharness": "lifecycleharness_test",
    "tests/testinfra/supervisorbuild": "supervisorbuild_test",
    "pkg/audit": "audit_test",
    "pkg/quota": "quota_test",
    "pkg/idempotency": "idempotency_test",
    "pkg/leaseextension": "leaseextension_test",
}

CONCURRENCY = SRC + "/concurrency/concurrency_test.go"
# The package each concurrency test exercises, by its import path's last
# element.
CONCURRENCY_IMPORTS = {
    "audit": "github.com/lennylabs/lenny/pkg/audit",
    "quota": "github.com/lennylabs/lenny/pkg/quota",
    "idempotency": "github.com/lennylabs/lenny/pkg/idempotency",
    "leaseextension": "github.com/lennylabs/lenny/pkg/leaseextension",
}


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def read_moves():
    moves = []
    for line in open(os.path.join(HERE, "unit-moves.tsv"), encoding="utf-8"):
        line = line.rstrip("\n")
        if line and not line.startswith("#"):
            src, dst = line.split("\t")
            moves.append((src, dst))
    return moves


def doc_comment(path):
    """Return the package comment of a doc.go as a file comment.

    The comment opened "Package <name> is" and now opens "This file is",
    and the tier numbers it named are written as the tiers' names.
    """
    text = open(path, encoding="utf-8").read()
    lines = [l for l in text.split("\n") if l.startswith("//") and not l.startswith("// SPDX")]
    body = "\n".join(lines)
    body = re.sub(r"^// Package \w+ (is|holds)", r"// This file \1", body, count=1)
    for old, new in [(r"tier-1 ", "unit-tier "), (r"a tier-7(\s+//)? scenario", r"a load\1 scenario"),
                     (r"tier-7 cloud-load", "load_cloud"),
                     (r"this(\s+//)? package asserts", r"this\1 file asserts"),
                     (r"The check in this package asserts", "The check in this file asserts")]:
        body = re.sub(old, new, body)
    return body.rstrip() + "\n"


def set_package(text, pkg):
    """Set the package clause and drop a package doc comment above it."""
    return re.sub(r"(?ms)^(?:// Package [^\n]*\n(?://[^\n]*\n)*)?package \w+\n", "package %s\n" % pkg, text, count=1)


def insert_file_comment(text, comment):
    """Insert a file comment after the import block, before the first declaration."""
    m = re.search(r"(?m)^import \((?:.|\n)*?^\)\n", text)
    if not m:
        m = re.search(r"(?m)^import [^\n]*\n", text)
    pos = m.end()
    return text[:pos] + "\n" + comment + text[pos:]


def split_concurrency(dsts):
    text = open(CONCURRENCY, encoding="utf-8").read()
    funcs = re.findall(r"(?ms)^((?://[^\n]*\n)*func (Test\w+)\(.*?^}\n)", text)
    by_name = {name: body for body, name in funcs}
    comment = ("// This concurrency contract test runs the package's public surface from\n"
               "// N goroutines with the race detector on and asserts the documented\n"
               "// invariants hold under contention, as\n"
               "// `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`\n"
               "// does (TESTING.md §6.2).\n")
    for test, dst in dsts:
        body = by_name[test]
        pkg = PACKAGES[os.path.dirname(dst)]
        alias = pkg[: -len("_test")]
        imports = ['"testing"', '"sync"']
        if "json." in body:
            imports.append('"encoding/json"')
        if "time." in body:
            imports.append('"time"')
        std = "\n".join("\t" + i for i in sorted(imports))
        src = ("// SPDX-License-Identifier: MIT\n\npackage %s\n\nimport (\n%s\n\n\t\"%s\"\n)\n\n%s\n%s"
               % (pkg, std, CONCURRENCY_IMPORTS[alias], comment, body))
        with open(dst, "w", encoding="utf-8") as fh:
            fh.write(src)
        git("add", dst)
    git("rm", "-q", CONCURRENCY, os.path.join(SRC, "concurrency", "doc.go"))


def move_files(moves):
    for src, dst in moves:
        if "::" in src:
            continue
        git("mv", src, dst)
        pkg = PACKAGES[os.path.dirname(dst)]
        text = open(dst, encoding="utf-8").read()
        text = set_package(text, pkg)
        doc = os.path.join(os.path.dirname(src), "doc.go")
        if os.path.exists(doc):
            text = insert_file_comment(text, doc_comment(doc))
            git("rm", "-q", doc)
        with open(dst, "w", encoding="utf-8") as fh:
            fh.write(text)


def fix_moved_helpers():
    # cmd/lenny-gateway sits two directories below the root, where
    # tests/tier1_unit/flagdefaults sat three.
    p = "cmd/lenny-gateway/flagdefaults_test.go"
    s = open(p, encoding="utf-8").read()
    s = s.replace('filepath.Join(filepath.Dir(file), "..", "..", "..")', 'filepath.Join(filepath.Dir(file), "..", "..")')
    open(p, "w", encoding="utf-8").write(s)
    # The chart's RBAC test and helm-unittest driver share the values_test
    # package, so the RBAC test uses the driver's repoRoot.
    p = "pkg/chart/values/cluster_role_verbs_test.go"
    s = open(p, encoding="utf-8").read()
    s = re.sub(r"(?ms)^func repoRoot\(t \*testing\.T\) string \{\n.*?^}\n", "", s)
    s = s.replace('\t"runtime"\n', "")
    open(p, "w", encoding="utf-8").write(s.rstrip("\n") + "\n")


def rewrite_paths(moves):
    pairs = []
    for src, dst in moves:
        if "::" in src:
            src_file, test = src.split("::")
            pairs.append((src, dst + "::" + test))
        else:
            pairs.append((src, dst))
    pairs.append((SRC + "/lifecycle/...", "tests/testinfra/lifecycleharness/..."))
    pairs.append((SRC + "/supervisor/...", "tests/testinfra/supervisorbuild/..."))
    pairs.append((SRC + "/rbac/rbac_test.go", "pkg/chart/values/cluster_role_verbs_test.go"))
    lookup = dict(pairs)
    pattern = re.compile("|".join(re.escape(a) for a, _ in sorted(pairs, key=lambda p: -len(p[0]))))
    for path in git("ls-files").split("\n"):
        if not path or path.startswith(HISTORICAL) or "/testdata/" in "/" + path:
            continue
        # The record-path test holds a tier1_unit path as the input a
        # translation of the retired tier directories leaves alone.
        if path == "tests/docs/record_tier_paths_test.go":
            continue
        if not os.path.isfile(path) or os.path.islink(path):
            continue
        try:
            text = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        new = pattern.sub(lambda m: lookup[m.group(0)], text)
        if new != text:
            open(path, "w", encoding="utf-8").write(new)


def main():
    moves = read_moves()
    split_concurrency([(s.split("::")[1], d) for s, d in moves if "::" in s])
    move_files(moves)
    git("rm", "-q", os.path.join(SRC, "README.md"))
    for root, dirs, files in os.walk(SRC, topdown=False):
        if not files:
            os.rmdir(root)
    fix_moved_helpers()
    rewrite_paths(moves)
    dsts = [d for _, d in moves]
    subprocess.run(["gofmt", "-w", *dsts], check=True)


if __name__ == "__main__":
    main()
