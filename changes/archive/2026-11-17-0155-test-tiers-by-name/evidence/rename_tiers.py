#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Rename the numbered test tier directories to the names lenny-test gives them.

This is the mechanical part of change 0155, step S1. It runs from the root of
a clean checkout and does four things:

1. It moves each numbered tier directory with `git mv` to the tier's name:
   tests/tier0_static becomes tests/static, tests/tier7a_load_local becomes
   tests/load_local, and so on. tests/tier4_integration merges into the
   existing tests/integration, whose four files already carry the
   `integration` build tag. tests/tier1_unit stays where it is; step S3
   moves its tests beside the code they test.
2. It rewrites the Go package clauses of the moved packages. A package name
   follows the Go naming convention of lowercase letters without
   underscores, so tier5_e2e_kind becomes e2ekind while its directory is
   e2e_kind.
3. It rewrites every path and package reference to a moved directory in the
   tracked files outside the historical records.
4. It rewrites prose that names a tier by number to the tier's name where the
   pattern is unambiguous, and lists the remaining mentions for a manual
   pass. Tiers 1 to 4 share their numbers with the capacity tiers of the
   deployment model ("Tier 3 load"), so a mention of those numbers is
   rewritten only where the next word names a test tier or a test.

Prose is rewritten only in comments for source files (Go, shell, YAML, SQL,
Makefile, Dockerfile, and JavaScript), and in the body text of Markdown
outside headings, fenced code blocks, and link targets. String literals in
source files are left alone, because some of them are fixtures or capacity
tier values ("tier0" as an invalid capacity tier).

Usage: rename_tiers.py [--no-move] [--report <file>]
"""
import argparse
import os
import re
import subprocess
import sys

# (old directory, new directory, new package name)
TIERS = [
    ("tier0_static", "static", "static"),
    ("tier2_component", "component", "component"),
    ("tier3_contract", "contract", "contract"),
    ("tier4_integration", "integration", "integration"),
    ("tier5_e2e_kind", "e2e_kind", "e2ekind"),
    ("tier6_e2e_cloud", "e2e_cloud", "e2ecloud"),
    ("tier7a_load_local", "load_local", "loadlocal"),
    ("tier7b_load_kind", "load_kind", "loadkind"),
    ("tier8_chaos", "chaos", "chaos"),
    ("tier9_security", "security", "security"),
    ("tier10_conformance", "conformance", "conformance"),
    ("tier11_docs", "docs", "docs"),
    ("tier12_load_cloud", "load_cloud", "loadcloud"),
]

# The name of each numbered tier.
NAMES = {
    "0": "static", "1": "unit", "2": "component", "3": "contract",
    "4": "integration", "5": "e2e_kind", "6": "e2e_cloud", "7a": "load_local",
    "7b": "load_kind", "8": "chaos", "9": "security", "10": "conformance",
    "11": "docs", "12": "load_cloud",
}

# Words that, following "tier N", restate the tier's own name. The phrase
# "tier-8 chaos test" becomes "chaos test".
SYNONYMS = {
    "0": ["static"], "1": ["unit"], "2": ["component"], "3": ["contract"],
    "4": ["integration"],
    "5": ["e2e_kind", r"e2e \(kind\)", "e2e"],
    "6": ["e2e_cloud", r"e2e \(cloud\)", "e2e", "cloud"],
    "7a": ["load_local", r"load \(local\)", "load"],
    "7b": ["load_kind", r"load \(kind\)", "load"],
    "8": ["chaos"], "9": ["security"], "10": ["conformance"],
    "11": ["docs", "documentation", "doc"],
    "12": ["load_cloud", "cloud-load", "cloud load", r"load \(cloud\)", "cloud"],
}

# Numbers the capacity tiers of the deployment model share.
AMBIGUOUS = {"1", "2", "3", "4"}

# Words that, after an ambiguous "tier-N", show the phrase names a test tier.
TEST_NOUNS = {
    "test", "tests", "suite", "suites", "case", "cases", "harness", "smoke",
    "leg", "envtest", "check", "checks", "gate", "gates", "package",
    "packages", "coverage", "run", "runs", "scenario", "scenarios",
}

# Words that, after a hyphenated "tier-N", show the phrase is a noun rather
# than a modifier: "the tier-0 and tier-1 tests", "tier-2 fails".
NOUN_FOLLOWERS = {
    "and", "or", "to", "through", "is", "are", "was", "were", "on", "in", "at",
    "with", "without", "rather", "fails", "fail", "passes", "pass", "green",
    "red", "onward", "onwards", "only", "alone", "itself", "also", "as", "so",
    "but", "then", "when", "where", "which", "that", "has", "have", "does",
    "do", "can", "will", "would", "should", "must", "may", "runs", "covers",
    "selects", "needs", "reads", "for", "of", "by", "from", "if", "because",
    "stays", "already", "first", "last", "still",
}

DETERMINERS = {
    "the", "a", "an", "each", "every", "this", "that", "its", "their", "any",
    "one", "no", "which", "whose", "per", "same", "lower", "higher", "own",
    "another", "same-tier", "whole", "full", "entire", "own", "our", "your",
}

# Verbs and prepositions that sit between a determiner and a tier noun.
LINKING = {
    "runs", "run", "in", "on", "at", "of", "to", "for", "from", "by", "with",
    "into", "through", "and", "or", "is", "are", "fails", "passes", "selects",
    "covers", "reaches", "skips", "needs", "reads", "after", "before",
}

HISTORICAL = (
    "changes/", "proposals/", "BUILD-GAPS.md", "TEST-GAPS.md", "CODE-REVIEW.md",
    "CHANGELOG.md", "gateway-runtime-comms", "PROPOSAL-QUEUE.md",
)

TEXT_SUFFIXES = (
    ".go", ".md", ".yaml", ".yml", ".json", ".sh", ".py", ".txt", ".sql",
    ".mjs", ".js", ".ts", ".toml", ".proto", ".tpl", ".bash", ".cfg", ".ini",
    ".mod",
)
TEXT_NAMES = ("Makefile", "Dockerfile", ".editorconfig", ".golangci.yml",
              ".gitignore", "CODEOWNERS")

TIER_WORD = r"(?<![-#/\w.])([Tt]ier)([ -])(7a|7b|1[0-2]|[0-9])(?![\w/+–]|-\d|-[a-z]+-|\s*(?:–|-|to|through)\s*\d)"


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True,
                          text=True).stdout


def is_text(path):
    base = os.path.basename(path)
    return path.endswith(TEXT_SUFFIXES) or base in TEXT_NAMES or base.startswith("Dockerfile")


def move_dirs():
    for old, new, _ in TIERS:
        src = "tests/" + old
        dst = "tests/" + new
        if not os.path.isdir(src):
            continue
        if os.path.exists(dst):
            for entry in sorted(os.listdir(src)):
                target = os.path.join(dst, entry)
                if os.path.exists(target):
                    sys.exit("refusing to merge %s: %s exists" % (src, target))
                git("mv", os.path.join(src, entry), target)
            os.rmdir(src)
        else:
            git("mv", src, dst)
    # Fixture trees under testdata/ mirror the tier directories by name.
    olds = {o: n for o, n, _ in TIERS}
    seen = set()
    for path in git("ls-files").split("\n"):
        parts = path.split("/")
        for i, part in enumerate(parts[:-1]):
            if part in olds and "testdata" in parts[:i]:
                d = "/".join(parts[:i + 1])
                if d not in seen:
                    seen.add(d)
                    git("mv", d, "/".join(parts[:i] + [olds[part]]))
                break


SKIP_REGISTER = "tests/registers/skip-reasons.yaml"


def rewrite_skip_reasons(text):
    """Rewrite the skip reasons the baseline register carries.

    The register holds the reason text of each skip verbatim, and the Go
    string literals of those skips are rewritten for the unambiguous tier
    numbers, so the register's copies are rewritten the same way. A leading
    quote stands in for the literal's opening quote, so the article rule
    reads the same context in both places.
    """
    out = []
    for line in text.split("\n"):
        m = re.match(r"(\s*(?:reason: )?)(.*)$", line)
        lead, value = m.group(1), m.group(2)
        if line.lstrip().startswith(("#", "- path:", "path:", "function:", "site:", "call:")):
            out.append(line)
            continue
        out.append(lead + rewrite_prose_segment('"' + value, False, True)[1:])
    return "\n".join(out)


SECTION_REGISTER = "tests/registers/spec-section-citations.yaml"


def merge_section_register():
    """Merge the counts of tests/integration and the renamed integration tier.

    The register keys a count by package directory, and the merge of
    tests/tier4_integration into tests/integration makes one directory of
    two. The entries are written back in sorted order, as the ratchet
    writes them.
    """
    text = open(SECTION_REGISTER, encoding="utf-8").read()
    head, sep, body = text.partition("packages:\n")
    entries = re.findall(r"    - package: (\S+)\n      count: (\d+)\n", body)
    counts = {}
    for pkg, count in entries:
        counts[pkg] = counts.get(pkg, 0) + int(count)
    out = "".join("    - package: %s\n      count: %d\n" % (k, counts[k]) for k in sorted(counts))
    with open(SECTION_REGISTER, "w", encoding="utf-8") as fh:
        fh.write(head + sep + out)


# ---- package clauses and path references ---------------------------------

PATH_RE = re.compile(r"\btests/(" + "|".join(o for o, _, _ in TIERS) + r")\b")
TOKEN_RE = re.compile(r"(?<![\w/])(" + "|".join(o for o, _, _ in TIERS) + r")(_test)?\b")
DIR = {o: n for o, n, _ in TIERS}
PKG = {o: p for o, _, p in TIERS}


def rewrite_refs(text, is_go):
    text = PATH_RE.sub(lambda m: "tests/" + DIR[m.group(1)], text)

    def token(m):
        old, test = m.group(1), m.group(2)
        if test:
            return PKG[old] + "_test"
        if is_go:
            line_start = text.rfind("\n", 0, m.start()) + 1
            before = text[line_start:m.start()]
            after = text[m.end():m.end() + 2]
            if before.strip() == "package" or (after[:1] == "." and after[1:2].isalpha()):
                return PKG[old]
        return DIR[old]

    return TOKEN_RE.sub(token, text)


# ---- prose -----------------------------------------------------------------

def article_for(before, capital):
    """Return the article to put before "<name> tier" given the text before."""
    words = re.findall(r"[\w'-]+|\(", before[-40:])
    prev = words[-1].lower() if words else ""
    if prev == "(" or before.rstrip().endswith("(") or prev in DETERMINERS or prev.endswith("'s"):
        return ""
    # "the companion Tier 7b" keeps its determiner: the word before the
    # tier is a modifier, not a verb or a preposition.
    prev2 = words[-2].lower() if len(words) > 1 else ""
    if prev2 in DETERMINERS and prev not in LINKING:
        return ""
    stripped = before.rstrip()
    sentence_start = (stripped == "" or re.search(r"([.!?:;]|//|#|\*|^-|\d\.|\|)$", stripped) is not None)
    if capital and sentence_start:
        return "The "
    return "the "


def rewrite_prose_segment(seg, allow_ambiguous_noun, unambiguous_only=False):
    out = []
    pos = 0
    for m in re.finditer(TIER_WORD, seg):
        num = m.group(3)
        if unambiguous_only and num in AMBIGUOUS:
            continue
        sep = m.group(2)
        capital = m.group(1)[0] == "T"
        rest = seg[m.end():]
        before = seg[:m.start()]
        name = NAMES[num] if num != "7" else None
        if name is None:
            continue
        # Rule A: "tier-8 chaos" or "Tier 0 (static)" restates the name.
        syn_done = False
        for syn in SYNONYMS[num]:
            ma = re.match(r"(\s+)(\(?`?)" + syn + r"(`?\)?)(?![\w-])", rest, re.I)
            if ma:
                paren = ma.group(2).startswith("(")
                if paren and not ma.group(3).endswith(")"):
                    continue
                out.append(seg[pos:m.start()])
                if paren:
                    art = article_for(before, capital)
                    add(out, art + name + " tier")
                else:
                    word = name
                    if capital and article_for(before, True) == "The ":
                        word = name[:1].upper() + name[1:] if "_" not in name else name
                    add(out, word)
                pos = m.end() + ma.end()
                syn_done = True
                break
        if syn_done:
            continue
        nxt = re.match(r"\s+([A-Za-z][\w-]*)", rest)
        nxt_word = nxt.group(1).lower() if nxt else ""
        # Rule B: a modifier, "tier-0 gate" becomes "static-tier gate", and
        # so does "tier-0" in "the tier-0 and tier-1 tests".
        paired = sep == "-" and re.match(r"\s+(?:and|or)\s+[Tt]ier-", rest) is not None
        if paired and num in AMBIGUOUS and not allow_ambiguous_noun:
            continue
        if paired or (nxt and (sep == "-" or nxt_word in TEST_NOUNS) and nxt_word not in NOUN_FOLLOWERS):
            if not paired and num in AMBIGUOUS and (capital or nxt_word not in TEST_NOUNS):
                continue
            out.append(seg[pos:m.start()])
            add(out, name + "-tier")
            pos = m.end()
            continue
        # Rule C: a noun, "run tier 0" becomes "run the static tier".
        if num in AMBIGUOUS and not allow_ambiguous_noun:
            continue
        out.append(seg[pos:m.start()])
        add(out, article_for(before, capital) + name + " tier")
        pos = m.end()
    out.append(seg[pos:])
    return "".join(out)


def add(out, replacement):
    """Append a replacement, turning a preceding "a" into "an" before a vowel."""
    prev = out[-1] if out else ""
    if replacement[:1].lower() in "aeiou" and re.search(r"(?:^|[^\w])[Aa] $", prev):
        out[-1] = prev[:-1] + "n "
    out.append(replacement)


def go_comment_start(line, state):
    """Return the index of a // comment in a Go line, or -1, tracking raw strings."""
    i = 0
    n = len(line)
    while i < n:
        c = line[i]
        if state["raw"]:
            if c == "`":
                state["raw"] = False
            i += 1
            continue
        if c == "`":
            state["raw"] = True
        elif c == '"' or c == "'":
            q = c
            i += 1
            while i < n and line[i] != q:
                if line[i] == "\\":
                    i += 1
                i += 1
        elif line.startswith("//", i):
            return i
        i += 1
    return -1


def hash_comment_start(line):
    quote = None
    for i, c in enumerate(line):
        if quote:
            if c == quote:
                quote = None
            continue
        if c in "\"'":
            quote = c
        elif c == "#" and (i == 0 or line[i - 1] in " \t"):
            return i
    return -1


# An interpreted Go string literal on one line.
STRING_RE = re.compile(r'"(?:[^"\\\n]|\\.)*"')


def rewrite_prose(path, text, allow_ambiguous_noun, strings=False):
    """Rewrite tier numbers in the prose of one file.

    With strings set, Go string literals are rewritten too, for the
    unambiguous tier numbers only.
    """
    lines = text.split("\n")
    base = os.path.basename(path)
    if path.endswith(".md"):
        fenced = False
        for i, line in enumerate(lines):
            if line.lstrip().startswith("```"):
                fenced = not fenced
                continue
            if fenced or line.lstrip().startswith("#"):
                continue
            # Leave link targets alone: rewrite outside "](...)".
            parts = re.split(r"(\]\([^)]*\))", line)
            lines[i] = "".join(p if p.startswith("](") else
                               rewrite_prose_segment(p, allow_ambiguous_noun)
                               for p in parts)
        return "\n".join(lines)
    if path.endswith((".go", ".mjs", ".js", ".ts", ".proto")):
        # A comment block that holds a quotation is left alone. The
        # annotation lint reads every comment group a branch changes for
        # quoted specification text, and a quotation written before the
        # specification had identifiers fails it once any line of its group
        # changes.
        quoted = set()
        block = []
        for i, line in enumerate(lines + [""]):
            if line.lstrip().startswith("//"):
                block.append(i)
                continue
            if any('"' in lines[j] for j in block):
                quoted.update(block)
            block = []
        state = {"raw": False}
        for i, line in enumerate(lines):
            raw_at_start = state["raw"]
            idx = go_comment_start(line, state)
            code, comment = (line[:idx], line[idx:]) if idx >= 0 else (line, "")
            if comment and i not in quoted and '"' not in comment:
                comment = rewrite_prose_segment(comment, allow_ambiguous_noun)
            if strings and not raw_at_start:
                code = STRING_RE.sub(lambda m: rewrite_prose_segment(m.group(0), False, True), code)
            lines[i] = code + comment
        return "\n".join(lines)
    if path.endswith(".sql"):
        for i, line in enumerate(lines):
            idx = line.find("--")
            if idx >= 0:
                lines[i] = line[:idx] + rewrite_prose_segment(line[idx:], allow_ambiguous_noun)
        return "\n".join(lines)
    if path.endswith((".sh", ".bash", ".yaml", ".yml", ".py", ".toml", ".cfg")) or \
            base in TEXT_NAMES or base.startswith("Dockerfile"):
        for i, line in enumerate(lines):
            idx = hash_comment_start(line)
            if idx >= 0:
                lines[i] = line[:idx] + rewrite_prose_segment(line[idx:], allow_ambiguous_noun)
        return "\n".join(lines)
    return text


# Files whose every "tier N" mention names a test tier, so the ambiguous
# numbers are rewritten in noun position as well. TESTING.md and
# TESTING_DEPENDENCIES.md are left out because they also name capacity tiers
# and platform support tiers, and so are the test directories, whose
# comments name the tiers of the ops lock store; their tier mentions are
# rewritten by hand.
TEST_CONTEXT = (
    ".claude/", "docs/testing/", "cmd/lenny-test/", "tests/README.md",
    ".github/",
)


# String literals that quote a historical record, which keeps its words. The
# test that holds the literal compares it with the record, so the literal
# keeps the old tier name as well.
PRESERVE = {
    "tests/static/spec_structure_records_test.go": [
        "`tests/tier0_static/doc_spec_gates_test.go`",
        "Tier 11: the documentation gates",
    ],
}


def preserve(path, new):
    for literal in PRESERVE.get(path, []):
        quoted = '"' + literal + '"'
        rewritten = rewrite_prose(path, rewrite_refs(quoted, True), False, True)
        new = new.replace(rewritten, quoted)
    return new


# Edits the rules above do not make: a stale path that never named a tier
# directory, a pattern that matched the old directory name, and a fixture
# sentence the article rule capitalizes wrongly.
FIXUPS = [
    ("tests/load_cloud/scenarios/session_pod_claim/main.js",
     "tests/tier7_load_cloud/", "tests/load_cloud/"),
    ("tests/docs/conformance_readme_skip_state_test.go",
     r"\btier10_conformance/\s*#", r"\bconformance/\s*#"),
    # The test holds the header the Makefile writes, whose comment the
    # Makefile pass rewrote.
    ("tests/docs/embedded_manifests_sync_test.go",
     "The tier-11 sync check\\n", "The docs-tier sync check\\n"),
    ("scripts/specshift/docgate/impact_test.go",
     "## Tests\\n\\n- the static tier.\\n", "## Tests\\n\\n- The static tier.\\n"),
]


def apply_fixups():
    for path, old, new in FIXUPS:
        text = open(path, encoding="utf-8").read()
        if old in text:
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(text.replace(old, new))


def in_scope(path):
    return not path.startswith(HISTORICAL)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--no-move", action="store_true")
    ap.add_argument("--report", default="")
    args = ap.parse_args()
    if not args.no_move:
        move_dirs()
    files = [p for p in git("ls-files").split("\n") if p]
    moved_dirs = tuple("tests/" + n + "/" for _, n, _ in TIERS)
    changed = 0
    rewritten = []
    for path in files:
        if not in_scope(path) or not os.path.isfile(path) or os.path.islink(path):
            continue
        try:
            text = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        new = rewrite_refs(text, path.endswith(".go"))
        prose_ok = is_text(path) and "/testdata/" not in "/" + path and not path.endswith(".json") \
            and not path.startswith("charts/") and not path.endswith(".txt")
        if prose_ok:
            ambiguous_ok = path.startswith(TEST_CONTEXT)
            if path.startswith("spec/"):
                ambiguous_ok = False
            # Go string literals hold log, skip, and failure messages. The
            # documentation tests compare string literals with records and
            # headings, so their literals are rewritten by hand.
            strings = path.endswith(".go") and not path.startswith("tests/docs/")
            new = rewrite_prose(path, new, ambiguous_ok, strings)
        if path.startswith("charts/") and is_text(path):
            # Comments in the chart name capacity tiers 1 to 3; only the
            # unambiguous test tier numbers are rewritten there.
            new = rewrite_prose(path, new, False)
        if path == SKIP_REGISTER:
            new = rewrite_skip_reasons(new)
        new = preserve(path, new)
        if new != text:
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(new)
            changed += 1
            rewritten.append(path)
    merge_section_register()
    apply_fixups()
    # A renamed import path can change the order of an import block, and a
    # rewritten literal can change the alignment of a composite literal, so
    # the rewritten Go files are formatted again.
    go_files = [p for p in rewritten if p.endswith(".go") and "/testdata/" not in "/" + p]
    for i in range(0, len(go_files), 200):
        subprocess.run(["gofmt", "-w", *go_files[i:i + 200]], check=True)
    print("rewrote %d files" % changed)
    if args.report:
        residue = re.compile(r"(?<![-#/\w.])[Tt]iers?[ -]?(?:7a|7b|1[0-2]|[0-9])\b")
        with open(args.report, "w", encoding="utf-8") as fh:
            for path in files:
                if not in_scope(path) or not is_text(path) or not os.path.isfile(path):
                    continue
                try:
                    text = open(path, encoding="utf-8").read()
                except UnicodeDecodeError:
                    continue
                for n, line in enumerate(text.split("\n"), 1):
                    if residue.search(line):
                        fh.write("%s:%d: %s\n" % (path, n, line.strip()[:220]))


if __name__ == "__main__":
    main()
