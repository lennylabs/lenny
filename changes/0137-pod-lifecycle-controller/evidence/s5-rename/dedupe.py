# SPDX-License-Identifier: MIT
"""Collapse the doubled PodSet names the kind rename leaves (0137.S5).

strings.py renames both SandboxTemplate and SandboxWarmPool to PodSet, so
a phrase that named both halves now names PodSet twice: "`PodSet` and
`PodSet`", ["podsets", "podsets"], "podsets/status, podsets/status". This
script collapses each such pair to one name. It runs on the same files as
strings.py and prints every rewrite with --dry-run for review.

    python3 changes/0137-pod-lifecycle-controller/evidence/s5-rename/dedupe.py --dry-run
    python3 changes/0137-pod-lifecycle-controller/evidence/s5-rename/dedupe.py
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import strings  # noqa: E402

SEP = r"(?:\s*(?:,|/|\+|and|or)\s*(?:and\s+|or\s+)?(?:the\s+|a\s+|its\s+)?)"


def q(word):
    # A name with optional backtick, double quote, or single quote around it.
    return r"(?P<o{n}>[`\"']?)" + word + r"(?P=o{n})"


PAIRS = []
for name in [r"PodSet(?:\.spec|\.status)?", r"podsets(?:/status)?", r"podsets\.lenny\.dev", r"podset", r"PodSets"]:
    a = q(f"(?P<w>{name})").format(n=1)
    b = q("(?P=w)").format(n=2)
    PAIRS.append(re.compile(a + SEP + b + r"(?![A-Za-z])"))


# Phrases the merge leaves behind: an article before the new vowel-initial
# kind name, and the "pair" a sentence named when two objects existed.
PHRASES = [
    (re.compile(r"\b([Aa]) AgentPod"), r"\1n AgentPod"),
    (re.compile(r"\bwarm-pool CRD pair\b"), "PodSet"),
    (re.compile(r"\bPodSet CRD pair\b"), "PodSet"),
    (re.compile(r"\bPodSet pair\b"), "PodSet"),
    (re.compile(r"\bCRD pair\b"), "PodSet"),
    (re.compile(r"\bpool pair\b"), "PodSet"),
]


def collapse(line):
    for rx, repl in PHRASES:
        line = rx.sub(repl, line)
    prev = None
    while prev != line:
        prev = line
        for rx in PAIRS:
            line = rx.sub(lambda m: m.group("o1") + m.group("w") + m.group("o1"), line)
    return line


def main(argv):
    dry = "--dry-run" in argv
    changed = 0
    for path in strings.tracked_files([a for a in argv if not a.startswith("--")]):
        if not strings.in_scope(path) or not os.path.isfile(path):
            continue
        if "--no-go" in argv and path.endswith(".go"):
            continue
        try:
            src = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        lines = src.split("\n")
        go = path.endswith(".go")
        for i, line in enumerate(lines):
            if "odSet" not in line and "odset" not in line and "AgentPod" not in line and "pair" not in line:
                continue
            if go and strings.SPEC_IN_BODY.match(line):
                continue
            new = collapse(line)
            if new != line:
                if dry:
                    print(f"{path}:{i+1}: {line.strip()[:150]}\n    -> {new.strip()[:150]}")
                lines[i] = new
        out = "\n".join(lines)
        if out != src:
            changed += 1
            if not dry:
                open(path, "w", encoding="utf-8").write(out)
    print(f"{'would change' if dry else 'changed'} {changed} files")


if __name__ == "__main__":
    main(sys.argv[1:])
