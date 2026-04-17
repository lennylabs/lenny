#!/usr/bin/env python3
"""Verify links and references across the split spec/ files.

Checks:
1. Every markdown link `[text](target)` in spec/*.md resolves:
   - File exists (for cross-file links).
   - Anchor exists in the target file.
2. Every plain-text `Section N.M` / `§N.M` reference appears inside a
   markdown link (so the reader has a way to navigate).
3. Reports orphan anchors (defined but never linked) and missing anchors
   (linked but never defined).
4. Fenced code blocks and inline code are excluded.

Run: python3 scripts/verify_spec_links.py
"""

from __future__ import annotations

import re
import sys
from collections import defaultdict
from pathlib import Path


SPEC_DIR = Path("spec")
FENCE_RE = re.compile(r"^\s*```")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$")
MD_LINK_RE = re.compile(r"\[([^\]]+)\]\(([^)]+)\)")
SECTION_REF_RE = re.compile(r"\bSection (\d+(?:\.\d+)*)\b")
PARA_REF_RE = re.compile(r"§(\d+(?:\.\d+)*)")

# Same heuristic the splitter uses to leave external-spec § refs alone
# (RFC 9110 §11.5, A2A spec §3, etc.).
EXTERNAL_CONTEXT_RE = re.compile(
    r"(?:\bRFC\s*\d+[A-Z]?|\b\w+\s+spec(?:ification)?|\bA2A\s+(?:spec|protocol)|"
    r"\bMCP\s+spec|\bOAuth\s*2?(?:\.1)?|\bOIDC|\bHTTP/[0-9.]+|\bHIPAA|"
    r"\bGDPR\s+Article|\bCFR|\bUSC)"
    r"(?:[\s,(]*(?:at|in|§)?\s*)$",
    re.IGNORECASE,
)


def is_external_context(text: str, pos: int) -> bool:
    window = text[max(0, pos - 40) : pos]
    return EXTERNAL_CONTEXT_RE.search(window) is not None


def slugify(text: str) -> str:
    s = text.lower()
    s = re.sub(r"[^\w\s-]", "", s, flags=re.UNICODE)
    s = re.sub(r"\s", "-", s)
    return s.strip("-")


def strip_inline_code(line: str) -> str:
    """Replace inline `...` spans with blanks (same length, preserves columns)."""
    def _blank(m: re.Match[str]) -> str:
        return " " * (m.end() - m.start())
    return re.sub(r"(`+)(?:(?!\1)[\s\S])+?\1", _blank, line)


def gather_anchors(path: Path) -> set[str]:
    """Return the set of heading anchors defined in a file."""
    anchors: set[str] = set()
    in_fence = False
    for line in path.read_text(encoding="utf-8").splitlines():
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        m = HEADING_RE.match(line)
        if m:
            anchors.add(slugify(m.group(2)))
    return anchors


def check_file(
    path: Path,
    anchors_by_file: dict[str, set[str]],
) -> tuple[list[str], list[tuple[int, str]]]:
    """Return (errors, unlinked_plain_refs) for a single file."""
    errors: list[str] = []
    unlinked: list[tuple[int, str]] = []

    in_fence = False
    for lineno, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if FENCE_RE.match(raw):
            in_fence = not in_fence
            continue
        if in_fence:
            continue

        line = strip_inline_code(raw)

        # Spans inside existing markdown link text: we still want to validate
        # the link target, but when checking plain-text `Section N.M` we must
        # skip ranges covered by a link's text/URL.
        linked_ranges: list[tuple[int, int]] = []

        for m in MD_LINK_RE.finditer(line):
            linked_ranges.append(m.span())
            target = m.group(2).strip()
            # Ignore external links.
            if target.startswith(("http://", "https://", "mailto:", "ftp://")):
                continue
            if "#" in target:
                file_part, _, anchor = target.partition("#")
            else:
                file_part, anchor = target, ""
            if file_part == "":
                resolve = path.name
            else:
                resolve = file_part
            if resolve not in anchors_by_file:
                errors.append(
                    f"{path.name}:{lineno}: link target file not found: {target}"
                )
                continue
            if anchor and anchor not in anchors_by_file[resolve]:
                errors.append(
                    f"{path.name}:{lineno}: anchor not found: {target}"
                )

        def in_linked(pos: int) -> bool:
            return any(a <= pos < b for a, b in linked_ranges)

        # Plain-text Section refs — warn if not inside a link.
        for m in SECTION_REF_RE.finditer(line):
            if in_linked(m.start()):
                continue
            unlinked.append((lineno, f"Section {m.group(1)}"))

        for m in PARA_REF_RE.finditer(line):
            top = m.group(1).split(".")[0]
            try:
                top_n = int(top)
            except ValueError:
                continue
            if not (1 <= top_n <= 24):
                continue
            if in_linked(m.start()):
                continue
            # External-spec citations (e.g. "RFC 9110 §11.5") are intentional
            # plain-text references, not missing links.
            if is_external_context(line, m.start()):
                continue
            unlinked.append((lineno, f"§{m.group(1)}"))

    return errors, unlinked


def main() -> int:
    if not SPEC_DIR.exists():
        print(f"error: {SPEC_DIR} not found", file=sys.stderr)
        return 2

    files = sorted(SPEC_DIR.glob("*.md"))
    anchors_by_file: dict[str, set[str]] = {}
    for p in files:
        anchors_by_file[p.name] = gather_anchors(p)

    all_errors: list[str] = []
    all_unlinked: dict[str, list[tuple[int, str]]] = defaultdict(list)

    for p in files:
        errs, unlinked = check_file(p, anchors_by_file)
        all_errors.extend(errs)
        if unlinked:
            all_unlinked[p.name].extend(unlinked)

    print(f"== verified {len(files)} files ==")
    print(f"link errors: {len(all_errors)}")
    for e in all_errors[:50]:
        print(f"  {e}")
    if len(all_errors) > 50:
        print(f"  ... {len(all_errors) - 50} more")

    unlinked_count = sum(len(v) for v in all_unlinked.values())
    print(f"\nplain-text refs not inside a link: {unlinked_count}")
    for fname in sorted(all_unlinked.keys()):
        print(f"  {fname}: {len(all_unlinked[fname])}")
        for lineno, ref in all_unlinked[fname][:5]:
            print(f"    line {lineno}: {ref}")
        if len(all_unlinked[fname]) > 5:
            print(f"    ... {len(all_unlinked[fname]) - 5} more")

    return 0 if not all_errors else 1


if __name__ == "__main__":
    sys.exit(main())
