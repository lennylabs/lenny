#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Replace a citation of a proposal's own numbering with the specification it meant.

WHY THESE ARE WRONG. `§3.4` in a Go comment sends a reader to
spec/03_high-level-architecture.md, which carries no numbered subsections at all.
The number belongs to a proposal: `### 3.4 The recycle disposition` in
proposals/0002. The reference is not stale, it points at the wrong document, and
the project's conventions forbid a proposal-internal identifier in product code
outright.

WHY THE MAPPING IS RECOVERABLE. A proposal's change entries name their target, so
`### 6.39` in proposals/0002 reads "`spec/06_warm-pod-model.md` §6.2 (host-node
schedulability ...)". The specification section a citation meant is therefore
readable out of the proposal that declared the number.

WHY IT IS NOT ONE LOOKUP. The same number is declared by several proposals: `§3.4`
appears in 0002, 0010, 0011 and 0013, each with different content. The number alone
does not identify a proposal, so the citing sentence has to disambiguate, and that
is a reading rather than a substitution.

Each site is presented with its surrounding lines and every candidate heading that
declares its number. The reviewer returns the specification section the citation
meant, or says it has no specification home — which is a finding about the
specification, not about the citation, and is reported rather than papered over.

Usage:
    author-proposal-refs.py --sites <phase2/sites.json> --out <dir>
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
Each item below is a line of this repository citing a section number that belongs
to a PROPOSAL document, not to the specification. A reader following it lands in
the wrong document, and product code is not allowed to carry a proposal-internal
identifier. Decide what each citation should say.

You are given, per item: the citing line with the lines around it, and every
proposal heading that declares that number, since the same number is declared by
several proposals and only the sentence tells you which one is meant.

RETURN ONE DECISION PER ITEM:

  "rewrite"  — give the exact replacement text for the marked line, citing the
               SPECIFICATION section the sentence actually depends on. Prefer the
               section the matching proposal heading names as its target: a
               heading reading "`spec/06_warm-pod-model.md` §6.2 (host-node
               schedulability)" means the citation should say §6.2. Preserve
               leading whitespace, the comment marker, and any trailing
               continuation exactly.
  "drop"     — the pointer can go and the sentence stands without it. Give the
               replacement line with the citation removed and the prose repaired.
               Use this when no specification section states the rule and the
               sentence does not need a pointer to make sense.
  "gap"      — the sentence depends on a rule the specification does not state.
               Do not invent a section. Say in one clause what the rule is and
               which proposal states it. Nothing is applied for these; they are
               collected for a human.
  "leave"    — the number is not a citation at all (a version, a quantity, a
               literal in data, a test's expected string), or you cannot tell what
               is meant. Say why.

RULES:
  - Never cite a specification section you have not seen named. If the proposal
    heading does not name a target and you cannot otherwise tell, that is "gap"
    or "leave", not a guess.
  - A line may carry several citations. Only the proposal-numbered one changes;
    a specification citation on the same line stays exactly as written.
  - Removing a citation must not strand a preposition, a separator, a possessive
    or a subject. Repair the sentence if it would.
  - Do not change what a line asserts. If the pointer was the only thing saying
    which rule applies, prefer "gap" over a rewrite that quietly narrows it.

OUTPUT. Return JSON and nothing else, on a single line:
{"decisions":[{"i":<0-based index>,"action":"rewrite|drop|gap|leave",
"line":"<exact replacement text, for rewrite and drop>","why":"<one clause>"}]}
Every item must appear exactly once.
"""

lock = threading.Lock()


def context_of(path, lineno, span=3):
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
        cands = "\n".join(f"      {f}  declares §{it['section']} as: {t}" for f, t in it.get("candidates", []))
        ctx = "\n".join(
            f'    {n:>5}{" >>" if n == it["line"] else "   "} {t}'
            for n, t in context_of(it["file"], it["line"])
        )
        blocks.append(
            f'[{i}] {it["file"]}  (marked line {it["line"]}; the proposal-numbered citation is §{it["section"]})\n'
            f'    candidate declarers:\n{cands}\n{ctx}'
        )
    prompt = BRIEF + "\n\nBATCH:\n" + "\n\n".join(blocks)
    try:
        p = subprocess.run(["claude", "-p", prompt, "--model", model, "--effort", effort],
                           capture_output=True, text=True, timeout=900)
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
                fh.write(json.dumps({**items[idx], "action": d.get("action"),
                                     "line_text": d.get("line"), "why": d.get("why")}) + "\n")
                n += 1
        open(done_path, "a").write(batch["id"] + "\n")
    return n


def apply_decisions(out_path):
    rows = [json.loads(l) for l in open(out_path) if l.strip() and "error" not in json.loads(l)]
    by_file = {}
    for r in rows:
        by_file.setdefault(r["file"], []).append(r)
    tally = {"rewrite": 0, "drop": 0, "gap": 0, "leave": 0, "skipped": 0}
    for f, rs in by_file.items():
        try:
            lines = open(f, errors="ignore").read().splitlines(keepends=True)
        except OSError:
            continue
        for r in sorted(rs, key=lambda x: -x["line"]):
            i = r["line"] - 1
            act = r.get("action")
            # A replacement spanning more than one line cannot be written over a
            # single line: doing so leaves the original continuation standing and
            # the file stops parsing. Reject it rather than splice, and count it
            # as skipped so it is visible.
            if r.get("line_text") and "\n" in r["line_text"]:
                tally["skipped"] += 1
                continue
            if not (0 <= i < len(lines)) or act not in ("rewrite", "drop") or not r.get("line_text"):
                tally[act if act in tally else "skipped"] += 1
                continue
            lines[i] = r["line_text"].rstrip("\n") + "\n"
            tally[act] += 1
        open(f, "w").write("".join(lines))
    print("applied:", tally)
    if tally["skipped"]:
        print(f"  ({tally['skipped']} rejected: the replacement spanned more than one line)")
    gaps = [r for r in rows if r.get("action") == "gap"]
    if gaps:
        print(f"\nspecification gaps reported ({len(gaps)}), nothing applied:")
        for g in gaps[:15]:
            print(f"  {g['file']}:{g['line']}  §{g['section']} — {(g.get('why') or '')[:100]}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sites", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--effort", default="low")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--batch-size", type=int, default=12)
    ap.add_argument("--apply", action="store_true")
    args = ap.parse_args()

    out_path = os.path.join(args.out, "decisions.jsonl")
    done_path = os.path.join(args.out, "done.txt")
    if args.apply:
        apply_decisions(out_path)
        return

    bdir = os.path.join(args.out, "batches")
    os.makedirs(bdir, exist_ok=True)
    sites = json.load(open(args.sites))
    done = {l.strip() for l in open(done_path)} if os.path.exists(done_path) else set()
    todo = []
    for i in range(0, len(sites), args.batch_size):
        chunk = sites[i : i + args.batch_size]
        bid = hashlib.sha256(json.dumps(chunk, sort_keys=True).encode()).hexdigest()[:16]
        bp = os.path.join(bdir, f"{bid}.json")
        json.dump({"id": bid, "items": chunk}, open(bp, "w"), indent=1)
        if bid not in done:
            todo.append(bp)

    print(f"sites: {len(sites)}   batches: {len(todo)}  (skipping {len(done)})")
    total = 0
    with cf.ThreadPoolExecutor(max_workers=args.jobs) as ex:
        futs = [ex.submit(run_batch, p, args.model, args.effort, out_path, done_path) for p in todo]
        for k, f in enumerate(cf.as_completed(futs), 1):
            total += f.result()
            if k % 10 == 0 or k == len(todo):
                print(f"  {k}/{len(todo)} batches, {total} decisions", flush=True)
    print(f"\ndecisions: {total}  ->  {out_path}")


if __name__ == "__main__":
    sys.exit(main())
