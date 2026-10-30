#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Have a reviewer author the removal of each testing-document reference.

WHY AUTHORING RATHER THAN SUBSTITUTION. Three mechanical passes over this
population were tried and each produced damage the next check caught: a
user-facing string reduced to `see.`, a sentence left without its subject, a
separator joined to a separator, and — worst — a map key in a filename allowlist
treated as a citation, which would have changed what a validator checks.

The population does not admit a text rule. Of 223 sites, 194 are comment prose,
17 sit inside a user-facing or test string, 11 are other code context, and one is
a literal filename in a data structure. Nothing in the text separates the last 29
from the first 194, and even inside the prose a removal is an authoring job: a
sentence reading "§12.3.7 is explicit that ..." needs a new subject, not a
deletion.

So the judgement comes first and the mechanism second. Each site is presented with
the lines around it, and the reviewer returns one of: a rewritten line, a decision
to delete the line, or a decision to leave it alone with the reason. Only the
first two are applied, and the result is reviewed again afterwards.

Usage:
    author-testing-refs.py --refs <phase0/testing-refs.json> --out <dir>
                           [--model sonnet] [--effort low] [--jobs 8] [--apply]
"""

import argparse
import concurrent.futures as cf
import hashlib
import json
import os
import subprocess
import sys
import threading

BRIEF = """\
Product code in this repository should not point a reader at the project's testing
document. Each item below is a line that references it, either by name
("TESTING.md") or by a bare section number that belongs to it rather than to the
specification. Your job is to decide, for each, what the line should say instead.

CONTEXT LINES are given around each site so you can see what the sentence is
doing. Only the marked line may change.

RETURN ONE DECISION PER ITEM:

  "rewrite"  — give the exact replacement text for the marked line. Use this when
               the reference can go and the line still says what it said. You must
               keep the sentence grammatical: if removing the reference strands a
               preposition, a separator, a possessive or a subject, supply the
               words that repair it rather than leaving a fragment. Preserve the
               line's leading whitespace, its comment marker, and any trailing
               continuation such as `"+` exactly.
  "delete"   — the line's whole content was the reference and nothing else of value
               remains.
  "leave"    — the reference must stay. Use this when it is NOT a citation: a
               literal filename in a map, an allowlist entry, a path a tool reads,
               a test asserting on the string, or anything where removing the text
               changes behaviour rather than prose. Also use it when you cannot
               tell what the sentence meant. Say why in one clause.

RULES THAT MATTER HERE, each learned from a mechanical attempt that got it wrong:

  - A specification citation is NOT a testing reference. `§4.3`, `§15.4.6` and the
    like stay. Only the number belonging to the testing document goes.
  - `§4.3 / §12.2.4` must not become `§4.3 /`. Remove the separator with the
    member.
  - A user-facing string must still read correctly to a user. `see TESTING.md
    §13.6.` must not become `see.`.
  - A line whose reference sits inside a quoted string that a test compares
    against is behaviour: leave it.
  - Do not invent a specification section to replace the testing reference. If the
    sentence needs a pointer it no longer has, prefer naming the thing in words.

OUTPUT. Return JSON and nothing else, on a single line:
{"decisions":[{"i":<0-based index>,"action":"rewrite|delete|leave",
"line":"<exact replacement text, only when action is rewrite>",
"why":"<one clause>"}]}
Every item in the batch must appear exactly once.
"""

lock = threading.Lock()


SECRE = __import__("re").compile(r"§(\d+(?:\.\d+)*(?:\.[a-z])?)")
_HD = __import__("re").compile(r"^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]")


def _declared(paths):
    out = set()
    for f in paths:
        try:
            for line in open(f, errors="ignore"):
                m = _HD.match(line)
                if m:
                    out.add(m.group(1).rstrip("."))
        except OSError:
            pass
    return out


_SPEC = None
_TEST = None


def owners_for(text):
    """Which document declares each §-number on the line."""
    global _SPEC, _TEST
    if _SPEC is None:
        ls = lambda p: subprocess.run(["git", "ls-files", p], capture_output=True, text=True).stdout.split()
        _SPEC = _declared(ls("spec/*.md"))
        _SPEC |= {n.split(".")[0] for n in _SPEC}
        _TEST = _declared(["TESTING.md"])
    out = {}
    for n in SECRE.findall(text):
        base = n.rstrip("abcdefghijklmnopqrstuvwxyz.")
        where = []
        if base in _SPEC:
            where.append("the specification")
        if base in _TEST:
            where.append("TESTING.md")
        out[n] = " and ".join(where) if where else "no document in this repository"
    return out


def context_of(path, lineno, span=2):
    try:
        lines = open(path, errors="ignore").read().splitlines()
    except OSError:
        return []
    lo, hi = max(0, lineno - 1 - span), min(len(lines), lineno + span)
    return [(n + 1, lines[n]) for n in range(lo, hi)]


def run_batch(bpath, model, effort, out_path, done_path):
    batch = json.load(open(bpath))
    items = batch["items"]
    blocks = []
    for i, it in enumerate(items):
        # Say which document declares each number on the line. Without this the
        # reviewer guesses, and it guessed wrong: it called a testing-document
        # section a specification citation and left it, on the reasonable
        # assumption that a §-number means the specification.
        owners = it.get("owners") or {}
        owned = "".join(f"\n    §{k} is declared by {v}" for k, v in sorted(owners.items()))
        ctx = "\n".join(
            f'    {n:>5}{" >>" if n == it["line"] else "   "} {t}'
            for n, t in context_of(it["file"], it["line"])
        )
        blocks.append(f'[{i}] {it["file"]}  (the marked line is {it["line"]}){owned}\n{ctx}')
    prompt = BRIEF + "\n\nBATCH:\n" + "\n\n".join(blocks)
    try:
        p = subprocess.run(
            ["claude", "-p", prompt, "--model", model, "--effort", effort],
            capture_output=True, text=True, timeout=900,
        )
        raw = p.stdout.strip()
        s, e = raw.find("{"), raw.rfind("}")
        parsed = json.loads(raw[s : e + 1]) if s >= 0 and e > s else {"decisions": []}
    except Exception as ex:
        with lock:
            open(out_path, "a").write(json.dumps({"batch": batch["id"], "error": str(ex)[:200]}) + "\n")
        return 0
    n = 0
    with lock:
        with open(out_path, "a") as fh:
            for d in parsed.get("decisions", []):
                idx = d.get("i")
                if not isinstance(idx, int) or not (0 <= idx < len(items)):
                    continue
                it = items[idx]
                fh.write(json.dumps({**it, "action": d.get("action"),
                                     "line_text": d.get("line"), "why": d.get("why")}) + "\n")
                n += 1
        open(done_path, "a").write(batch["id"] + "\n")
    return n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--refs", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--effort", default="low")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--batch-size", type=int, default=12)
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()

    bdir = os.path.join(args.out, "batches")
    os.makedirs(bdir, exist_ok=True)
    out_path = os.path.join(args.out, "decisions.jsonl")
    done_path = os.path.join(args.out, "done.txt")

    if args.apply:
        apply_decisions(out_path)
        return

    refs = [r for r in json.load(open(args.refs))
            if r["file"].startswith(("pkg/", "cmd/", "charts/", "migrations/"))]
    # One record per site; a line naming the document and its section is one job.
    seen, sites = set(), []
    for r in sorted(refs, key=lambda x: (x["file"], x["line"])):
        k = (r["file"], r["line"])
        if k in seen:
            continue
        seen.add(k)
        sites.append({"file": r["file"], "line": r["line"], "text": r["text"],
                      "owners": owners_for(r["text"])})

    done = {l.strip() for l in open(done_path)} if os.path.exists(done_path) else set()
    todo = []
    for i in range(0, len(sites), args.batch_size):
        chunk = sites[i : i + args.batch_size]
        bid = hashlib.sha256(json.dumps(chunk, sort_keys=True).encode()).hexdigest()[:16]
        bp = os.path.join(bdir, f"{bid}.json")
        json.dump({"id": bid, "items": chunk}, open(bp, "w"), indent=1)
        if bid not in done:
            todo.append(bp)

    print(f"sites: {len(sites)}   batches to run: {len(todo)}  (skipping {len(done)})")
    total = 0
    with cf.ThreadPoolExecutor(max_workers=args.jobs) as ex:
        futs = [ex.submit(run_batch, p, args.model, args.effort, out_path, done_path) for p in todo]
        for k, f in enumerate(cf.as_completed(futs), 1):
            total += f.result()
            if k % 5 == 0 or k == len(todo):
                print(f"  {k}/{len(todo)} batches, {total} decisions", flush=True)
    print(f"\ndecisions: {total}  ->  {out_path}\nrun again with --apply to write them")


def apply_decisions(out_path):
    """Write the rewrites and deletions, bottom-up so line numbers stay valid."""
    rows = [json.loads(l) for l in open(out_path) if l.strip() and "error" not in json.loads(l)]
    by_file = {}
    for r in rows:
        by_file.setdefault(r["file"], []).append(r)
    applied = {"rewrite": 0, "delete": 0, "leave": 0, "skipped": 0}
    for f, rs in by_file.items():
        try:
            lines = open(f, errors="ignore").read().splitlines(keepends=True)
        except OSError:
            continue
        for r in sorted(rs, key=lambda x: -x["line"]):
            i = r["line"] - 1
            if not (0 <= i < len(lines)):
                applied["skipped"] += 1
                continue
            if r["action"] == "delete":
                del lines[i]
                applied["delete"] += 1
            elif r["action"] == "rewrite" and r.get("line_text"):
                lines[i] = r["line_text"].rstrip("\n") + "\n"
                applied["rewrite"] += 1
            else:
                applied["leave"] += 1
        open(f, "w").write("".join(lines))
    print("applied:", applied)


if __name__ == "__main__":
    sys.exit(main())
