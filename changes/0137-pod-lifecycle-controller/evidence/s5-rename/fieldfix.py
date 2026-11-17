# SPDX-License-Identifier: MIT
"""Rename the struct fields of PLC-2 at the sites the Go type checker finds.

goident.go renames identifiers that carry a retired kind name. A field
whose name does not, such as AgentPodSpec.PoolRef (now PodSetRef), cannot
be renamed by name, because other structs carry fields of the same name.
This script reads the type checker's errors (from the go/packages loader
in typecheck/), keeps those that name the old field on the renamed
struct, and rewrites the field identifier at the reported line and
column. Run it until it reports no edits:

    (cd changes/0137-pod-lifecycle-controller/evidence/s5-rename/typecheck && go run . "$OLDPWD" ./...) > /tmp/errs.txt
    python3 changes/0137-pod-lifecycle-controller/evidence/s5-rename/fieldfix.py /tmp/errs.txt
"""
import re
import sys

# (old field, struct type the error names, new field)
FIELDS = [
    ("PoolRef", "AgentPodSpec", "PodSetRef"),
]

ERR = re.compile(r"^(?P<file>[^:]+\.go):(?P<line>\d+):(?P<col>\d+): (?P<msg>.*)$")


def main(path):
    edits = {}
    for raw in open(path):
        m = ERR.match(raw.strip())
        if not m:
            continue
        msg = m["msg"]
        for old, typ, new in FIELDS:
            undefined = f".{old} undefined" in msg and f".{typ} has no field or method {old}" in msg
            literal = msg.startswith(f"unknown field {old} in struct literal") and msg.endswith(f".{typ}")
            if undefined or literal:
                edits.setdefault(m["file"], set()).add((int(m["line"]), int(m["col"]), old, new))
    count = 0
    for f, items in edits.items():
        lines = open(f).read().split("\n")
        for line, col, old, new in sorted(items, reverse=True):
            text = lines[line - 1]
            i = text.find(old, max(col - 1 - len(old) - 40, 0))
            # Prefer the occurrence at or after the reported column, which
            # go/types sets to the selector or the composite-literal key.
            j = text.find(old, col - 1)
            if j >= 0:
                i = j
            if i < 0:
                print(f"no {old} at {f}:{line}", file=sys.stderr)
                continue
            lines[line - 1] = text[:i] + new + text[i + len(old):]
            count += 1
        open(f, "w").write("\n".join(lines))
    print(f"{count} field edits")


if __name__ == "__main__":
    main(sys.argv[1])
