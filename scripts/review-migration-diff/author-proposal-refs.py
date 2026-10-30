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
import re
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

GAP_BRIEF = """\
Each item below is a line of this repository citing a section number that belongs
to a PROPOSAL document. A previous reading established that the specification does
not state the rule these sentences depend on, so there is no section to point at.

That gap is being recorded separately and is not your problem. Yours is narrower:
the number must not stay. A proposal's internal numbering is meaningless to anyone
reading this repository, and it sends a reader to a specification section that
either does not exist or discusses something else entirely.

So remove the number and leave the sentence saying what it said, in words. A
comment reading "the §3.4 recycle disposition" becomes "the recycle disposition".
One reading "per §3.2, the coordinator holds the slot" becomes "the coordinator
holds the slot". The knowledge stays; only the false pointer goes.

RETURN ONE DECISION PER ITEM:

  "drop"     — give the replacement line with the proposal number removed and the
               sentence repaired. This is the expected action for nearly every
               item. Preserve leading whitespace, the comment marker, and any
               trailing continuation exactly.
  "rewrite"  — use this only if you can see a SPECIFICATION section named in the
               surrounding lines that plainly states the same rule. Give the
               replacement citing that section.
  "leave"    — the number is not a citation (a version, a quantity, a literal in
               data, a test's expected string), or the line is a table of
               contents for the document it sits in. Say why.

RULES:
  - Removing the number must not strand a preposition, an article, a separator or
    a possessive. "per §3.2, the" becomes "the", not ", the".
  - `§4.6.1 / §6.5` where only §6.5 is a proposal number becomes `§4.6.1`. Remove
    the separator with the number, and keep the specification citation.
  - A line reading `spec: §3.1, §5.2` keeps §5.2 and loses §3.1. If nothing is
    left after the marker, drop the marker too.
  - Do not weaken or generalise what the line asserts. The sentence must still
    make the same claim, minus the pointer.

OUTPUT. Return JSON and nothing else, on a single line:
{"decisions":[{"i":<0-based index>,"action":"drop|rewrite|leave",
"line":"<exact replacement text, for drop and rewrite>","why":"<one clause>"}]}
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


def run_batch(bpath, model, effort, out_path, done_path, brief=BRIEF):
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
    prompt = brief + "\n\nBATCH:\n" + "\n\n".join(blocks)
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


SECNUM = re.compile(r"§[\d.]+")


def is_subsequence(small, big):
    it = iter(big)
    return all(c in it for c in small)


def unsafe(before, after, target):
    """Why this replacement must not be written, or None when it is safe.

    Each check corresponds to a way an earlier run damaged the tree, and each is
    mechanical because the instruction not to do it did not prevent it: a
    reviewer told plainly never to write "spec §3.4" wrote it anyway.

    The last check is the general one. Removing a pointer only ever deletes
    characters, so a legitimate result is a subsequence of the line it replaces.
    Anything introducing a character is doing something other than what was
    asked, and the ways it has done so are not worth enumerating: one run copied
    the listing's line-number prefix into a document, another re-punctuated a
    clause it had stranded, and a third restructured a JSON tool schema so that a
    declared type moved inside the object it described. The subsequence test
    refuses all of them without having to anticipate any of them.
    """
    if re.search(r"spec[:\s]\s*§" + re.escape(target) + r"(?!\.?\d)", after):
        return "asserts the proposal's number is a specification section"
    b, a = SECNUM.findall(before), SECNUM.findall(after)
    added = set(a) - set(b)
    if added:
        return "introduces a citation that was not there: " + ", ".join(sorted(added))
    lost = [n for n in set(b) - set(a) if n.rstrip(".") != "§" + target]
    if lost:
        return "removes a citation it was not asked to touch: " + ", ".join(sorted(lost))
    if not is_subsequence(after.strip(), before.strip()):
        return "adds or reorders characters; removing a pointer only deletes"
    # Deleting the pointer can still leave the sentence holding punctuation that
    # belonged to it: an opening bracket whose subject has gone, a marker with
    # nothing after it, a separator against a separator. Each pattern is reported
    # only when the replacement introduces it, so a line that already read that
    # way is not blamed on this edit.
    for pat, what in STRANDED:
        if re.search(pat, after) and not re.search(pat, before):
            return what
    return None


STRANDED = [
    (r"[,;:.]\s+[(\[]", "leaves a separator against an opening bracket"),
    (r"[,;:]\s*[)\]]", "leaves a separator against a closing bracket"),
    (r"[(\[]\s*[,;:)\]]", "leaves a bracket with nothing in it"),
    (r"\b(?:spec|see|per)\s*:?\s*[(,;.]", "leaves a citation marker with nothing after it"),
    (r"^\s*(?://+|#+)\s*[),;:.]", "leaves the comment starting on punctuation"),
]


def merge_per_line(rows, residual):
    """Collapse several decisions about one line into a single replacement.

    A line carrying two proposal numbers is reviewed once per number, and each
    reading removes only the number it was given: for `the §3.2/§3.4 coordinator`
    one reviewer returns a line still holding §3.4 and the other one still holding
    §3.2. Writing both, in either order, restores the number the other removed.

    So the replacements are candidates rather than instructions. The one leaving
    the fewest of the line's targeted numbers behind wins, and if it still carries
    one, the line is recorded for a further reading instead of being written and
    called done.
    """
    by_line = {}
    for r in rows:
        by_line.setdefault(r["line"], []).append(r)
    out = []
    for line, group in by_line.items():
        if len(group) == 1:
            out.append(group[0])
            continue
        targets = {g["section"] for g in group}
        def left(g):
            t = g.get("line_text") or ""
            return sum(1 for s in targets
                       if re.search(r"§0*" + re.escape(s) + r"(?!\.?\d)", t))
        best = min(group, key=left)
        out.append(best)
        if left(best):
            residual.append({**best, "targets": sorted(targets)})
    return out


def apply_decisions(out_path):
    rows = [json.loads(l) for l in open(out_path) if l.strip() and "error" not in json.loads(l)]
    by_file = {}
    for r in rows:
        by_file.setdefault(r["file"], []).append(r)
    tally = {"rewrite": 0, "drop": 0, "gap": 0, "leave": 0, "skipped": 0}
    residual, refused = [], []
    for f, rs in by_file.items():
        try:
            lines = open(f, errors="ignore").read().splitlines(keepends=True)
        except OSError:
            continue
        rs = merge_per_line(rs, residual)
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
            # Take the indentation from the file rather than from the reply. The
            # site is shown inside a numbered listing, and a reviewer copying the
            # text back reproduces the listing's padding, or once its line-number
            # prefix, instead of the file's tabs.
            body = r["line_text"].rstrip("\n").lstrip(" \t")
            indent = lines[i][: len(lines[i]) - len(lines[i].lstrip(" \t"))]
            candidate = indent + body + "\n"
            why = unsafe(lines[i], candidate, r["section"])
            if why:
                refused.append({**r, "refused": why, "current": lines[i].strip()[:140]})
                tally["skipped"] += 1
                continue
            lines[i] = candidate
            tally[act] += 1
        open(f, "w").write("".join(lines))
    print("applied:", tally)
    if refused:
        print(f"\n{len(refused)} replacement(s) refused as unsafe:")
        for r in refused[:14]:
            print(f"  {r['file']}:{r['line']}  {r['refused']}")
            print(f"     -  {r['current'][:94]}")
            print(f"     +  {(r['line_text'] or '').strip()[:94]}")
    if residual:
        print(f"\n{len(residual)} line(s) still carry a targeted number after merging;"
              f" they need a further reading:")
        for r in residual[:10]:
            print(f"  {r['file']}:{r['line']}  targets {r['targets']}")
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
    # The gap mode revisits sites a first reading found have no specification
    # home. The pointer still has to go, so the brief asks for the number's
    # removal rather than for its resolution.
    ap.add_argument("--mode", choices=("resolve", "gaps"), default="resolve")
    args = ap.parse_args()
    brief = GAP_BRIEF if args.mode == "gaps" else BRIEF

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
        futs = [ex.submit(run_batch, p, args.model, args.effort, out_path, done_path, brief) for p in todo]
        for k, f in enumerate(cf.as_completed(futs), 1):
            total += f.result()
            if k % 10 == 0 or k == len(todo):
                print(f"  {k}/{len(todo)} batches, {total} decisions", flush=True)
    print(f"\ndecisions: {total}  ->  {out_path}")


if __name__ == "__main__":
    sys.exit(main())
