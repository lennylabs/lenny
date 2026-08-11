#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Run a batch of migration-diff hunks past a reviewer and persist what it flags.

WHAT THIS IS FOR. The funnel leaves two populations a mechanical rule cannot
decide: the lines where something other than a citation changed, and one exemplar
per distinct citation transformation. This drives a reviewer over both, in
parallel, and appends what it flags to a JSONL file.

IT FLAGS, IT DOES NOT FIX. Nothing here writes to the repository. A reviewer that
edited as it went would produce a tree nobody had read, which is the failure this
whole exercise exists to correct.

RESUMABLE BY CONSTRUCTION. A batch is named by the hash of its contents and a
completed batch id is recorded, so a re-run skips what is done and a crash costs
one batch. Results are appended per batch rather than written at the end.

Usage:
    review.py --dir <funnel-out> [--model sonnet] [--effort low] [--jobs 8]
              [--limit N] [--kind mixed|shape]
"""

import argparse
import concurrent.futures as cf
import json
import os
import subprocess
import sys
import threading

BRIEF = """\
You are reviewing one batch of changed lines from a large, mostly-mechanical
migration of a specification and the code that cites it. Report problems. Do not
propose rewrites, do not fix anything, and do not comment on style.

WHAT THE MIGRATION DID, so you can tell an intended change from a defect:

  1. It retired line-number citations. A citation that named a section together
     with a position inside it now names the section alone, or a numbered
     subsection of it. Losing the position is INTENDED and is never a finding on
     its own.
  2. It moved two blocks of specification content into a new section 28. Material
     that lived in section 4.7 (the intra-pod channel prose) and in section 15.4.1
     (the adapter-to-binary message schemas) now lives under section 28.5, so a
     pointer that used to name 4.7 or 15.4.1 may correctly now name 28.5.3.
  3. It renamed the wire identifiers of the adapter's control and
     runtime-operations channels, in their Go-symbol, manifest-key and
     command-flag spellings, to the canonical CH- names the register fixes.
     A rename from a retired spelling to a canonical one is intended.
  4. In artifacts whose text is served to clients (JSON schema descriptions, tool
     schemas), a citation is REMOVED rather than converted, because a client
     should not be shown a specification pointer. That is intended.

WHAT WENT WRONG BEFORE, so you know what to look for. Every one of these was
produced by a rule that deleted more than the citation:

  A. The sentence lost its own words. The rewrite consumed a phrase behind the
     citation that belonged to the sentence, not to the pointer. Symptoms: two
     articles collide ("is the §6.1 the grace period"); the sentence ends
     mid-clause; a noun is missing; a quoted term the sentence depended on is
     gone; two words are fused together ("enforces thetopology").
  B. A requirement identifier disappeared: NET-002, SCL-023, SEC-013, F-4.8.17
     and the like. These are greppable audit tokens, not decoration.
  C. A lettered sub-case disappeared: "(a)", "(b)", "(e)". The letter says which
     of a section's obligations the code implements, so losing it makes the
     citation name the whole section instead of the rule.
  D. A dangling separator or bracket was left behind: "§10.6 /.", "(§13.2 2)",
     an empty "()" or an unclosed quote.
  E. The citation written is well formed but wrong for its context: a raw file
     path inside running prose where a section reference belongs, or a markdown
     link whose visible label names a different section from its target.
  F. Something was renamed in a string that a test asserts on, or in an
     identifier, rather than in prose.

REPORT ONLY WHAT A READER OR A TOOL WOULD GET WRONG. A line that reads a little
flatter because a line number went away is not a finding. A line that now states
something false, states nothing, does not parse, or has lost a token another part
of the system looks up, is.

OUTPUT. Return JSON and nothing else, on a single line:
{"findings":[{"i":<0-based index of the item in this batch>,"class":"A|B|C|D|E|F",
"why":"<one sentence: what a reader or tool gets wrong>","confidence":"high|medium|low"}]}
An empty list is the expected answer for most batches. Return {"findings":[]} when
the batch is clean; do not invent a finding to appear thorough.
"""

SHAPE_NOTE = """\
THIS BATCH IS DIFFERENT. Each item is one EXEMPLAR of a transformation applied at
many sites; "applies_to_sites" says how many. Judge the transformation, not the
one line: if this rewrite is wrong, it is wrong at every site that shares it. That
makes a finding here worth more than a finding on a single line, and a false
positive here more expensive too.
"""


REMOVAL_NOTE = """\
THIS BATCH IS A REMOVAL, NOT A CITATION REWRITE. Each item is a line from product
code that referenced the project's testing document, either by name or by a bare
section number belonging to it, and the reference was deleted. Production code
should not point a reader at the testing document, so the deletion is the intent.

Judge only whether the deletion left the line correct:

  - Does the sentence still parse, and still say what it said minus the pointer?
    A dangling connective, a doubled article, a stranded punctuation mark, an
    orphaned label such as a bare "spec:", or a clause with no subject is a
    finding.
  - Was something removed that was NOT a reference to the testing document? A
    specification citation is not, and neither is an ordinary number.
  - Does the line still identify what it documents? Losing a pointer is fine;
    losing the name of the thing is not.
  - For a removed line, was the whole line really nothing but the reference?

Report a finding as class A when the line no longer parses or lost content, and
class F when the removal reached something that was not a testing reference.
"""

lock = threading.Lock()


def run_batch(path, model, effort, out_path, done_path):
    with open(path) as f:
        batch = json.load(f)
    items = batch["items"]
    listing = "\n".join(
        f'[{i}] {it.get("file","")}\n  BEFORE: {it.get("before","")}\n  AFTER:  {it.get("after","")}'
        + (f'\n  (this transformation applies to {it["applies_to_sites"]} sites)'
           if "applies_to_sites" in it else "")
        + (f'\n  (rule: {it["rule"]})' if "rule" in it else "")
        for i, it in enumerate(items)
    )
    extra = SHAPE_NOTE if batch["kind"] == "shape" else (REMOVAL_NOTE if batch["kind"] == "removal" else "")
    prompt = BRIEF + extra + "\n\nBATCH:\n" + listing
    try:
        p = subprocess.run(
            ["claude", "-p", prompt, "--model", model, "--effort", effort],
            capture_output=True, text=True, timeout=600,
        )
        raw = p.stdout.strip()
        s, e = raw.find("{"), raw.rfind("}")
        parsed = json.loads(raw[s : e + 1]) if s >= 0 and e > s else {"findings": []}
    except Exception as ex:
        with lock:
            with open(out_path, "a") as f:
                f.write(json.dumps({"batch": batch["id"], "error": str(ex)[:200]}) + "\n")
        return 0
    n = 0
    with lock:
        with open(out_path, "a") as f:
            for fd in parsed.get("findings", []):
                i = fd.get("i")
                it = items[i] if isinstance(i, int) and 0 <= i < len(items) else {}
                f.write(json.dumps({
                    "batch": batch["id"], "kind": batch["kind"],
                    "file": it.get("file"), "before": it.get("before"), "after": it.get("after"),
                    "applies_to_sites": it.get("applies_to_sites", 1),
                    "class": fd.get("class"), "why": fd.get("why"),
                    "confidence": fd.get("confidence"),
                }) + "\n")
                n += 1
        with open(done_path, "a") as f:
            f.write(batch["id"] + "\n")
    return n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True)
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--effort", default="low")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--kind", default="")
    args = ap.parse_args()

    bdir = os.path.join(args.dir, "batches")
    out_path = os.path.join(args.dir, "findings.jsonl")
    done_path = os.path.join(args.dir, "done.txt")
    done = set()
    if os.path.exists(done_path):
        done = {l.strip() for l in open(done_path) if l.strip()}

    todo = []
    for name in sorted(os.listdir(bdir)):
        bid = name[:-5]
        if bid in done:
            continue
        if args.kind:
            with open(os.path.join(bdir, name)) as f:
                if json.load(f)["kind"] != args.kind:
                    continue
        todo.append(os.path.join(bdir, name))
    if args.limit:
        todo = todo[: args.limit]

    print(f"batches to run: {len(todo)}  (skipping {len(done)} done)  model={args.model} effort={args.effort} jobs={args.jobs}")
    total = 0
    with cf.ThreadPoolExecutor(max_workers=args.jobs) as ex:
        futs = {ex.submit(run_batch, p, args.model, args.effort, out_path, done_path): p for p in todo}
        for k, fut in enumerate(cf.as_completed(futs), 1):
            total += fut.result()
            if k % 10 == 0 or k == len(todo):
                print(f"  {k}/{len(todo)} batches, {total} findings so far", flush=True)
    print(f"\nfindings: {total}  ->  {out_path}")


if __name__ == "__main__":
    sys.exit(main())
