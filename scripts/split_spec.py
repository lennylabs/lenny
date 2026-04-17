#!/usr/bin/env python3
"""Split SPEC.md into one file per top-level section under spec/.

- Each top-level section (`## N. Title`) becomes `spec/NN_slug.md`.
- The Table of Contents block and pre-amble become `spec/README.md`.
- Cross-references are rewritten to point across files:
    * Markdown anchor links `[text](#slug)` → `[text](NN_file.md#slug)` when the
      target anchor lives in a different section.
    * Plain-text `Section N` / `Section N.M` → `[Section N.M](NN_file.md#...)`.
    * `§N.M` paragraph-style refs are linked the same way.
    * `§164.xxx` HIPAA citations are left alone.
    * References inside fenced code blocks and inside existing markdown link
      text/URLs are left alone.

Usage: python3 scripts/split_spec.py [--source SPEC.md] [--dest spec]
"""

from __future__ import annotations

import argparse
import os
import re
import shutil
import sys
from dataclasses import dataclass, field
from pathlib import Path


HEADING_H2_RE = re.compile(r"^## (.+)$")
HEADING_H3_RE = re.compile(r"^### (.+)$")
HEADING_H4_RE = re.compile(r"^#### (.+)$")
TOP_SECTION_RE = re.compile(r"^(\d+)\.\s+(.+)$")
SUB_SECTION_RE = re.compile(r"^(\d+(?:\.\d+)+)\s+(.+)$")
FENCE_RE = re.compile(r"^\s*```")


def slugify(text: str) -> str:
    """Mimic GitHub's markdown heading anchor slug rules.

    Rules observed from the existing SPEC.md TOC:
    - Lower-case.
    - Drop punctuation except hyphens and spaces; hyphens are kept as-is.
    - Spaces become hyphens.
    - Multiple hyphens are preserved (e.g. `event--checkpoint-store`).
    - Leading/trailing hyphens stripped.
    """
    s = text.lower()
    # Keep alphanumerics, spaces, hyphens, and underscores; drop everything else.
    s = re.sub(r"[^\w\s-]", "", s, flags=re.UNICODE)
    # Convert each whitespace char to a hyphen (do NOT collapse runs — GitHub
    # preserves them, e.g. "Event / Checkpoint" → "event--checkpoint").
    s = re.sub(r"\s", "-", s)
    return s.strip("-")


def file_slug(text: str) -> str:
    """Filename-safe slug (collapses consecutive hyphens)."""
    return re.sub(r"-+", "-", slugify(text))


@dataclass
class Section:
    number: int           # 1..24
    title: str            # "Executive Summary"
    raw_heading: str      # "1. Executive Summary"
    file_slug: str        # "executive-summary"
    file_name: str        # "01_executive-summary.md"
    anchor: str           # "1-executive-summary" (intra-file anchor for the H2)
    start_line: int       # line index (0-based) of the `## ` line
    end_line: int = -1    # exclusive; line index at which next section starts
    sub_anchors: dict[str, str] = field(default_factory=dict)  # "4.1" -> "41-edge-gateway-replicas"


def parse_sections(lines: list[str]) -> tuple[list[Section], int, int]:
    """Find all top-level sections. Returns (sections, toc_start, toc_end).

    `toc_start`/`toc_end` bound the Table of Contents block (exclusive of
    surrounding `---` separators). If no TOC is found, both are -1.
    """
    sections: list[Section] = []
    toc_start = -1
    toc_end = -1

    in_fence = False
    pending_toc = False

    for i, line in enumerate(lines):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        h2 = HEADING_H2_RE.match(line)
        if not h2:
            continue
        heading = h2.group(1).strip()
        m = TOP_SECTION_RE.match(heading)
        if m:
            number = int(m.group(1))
            title = m.group(2).strip()
            fslug = file_slug(title)
            file_name = f"{number:02d}_{fslug}.md"
            anchor = slugify(heading)
            sections.append(
                Section(
                    number=number,
                    title=title,
                    raw_heading=heading,
                    file_slug=fslug,
                    file_name=file_name,
                    anchor=anchor,
                    start_line=i,
                )
            )
        elif heading.lower() == "table of contents":
            toc_start = i
            pending_toc = True

    # Compute end_line per section by looking at the next section's start.
    for idx, sec in enumerate(sections):
        if idx + 1 < len(sections):
            sec.end_line = sections[idx + 1].start_line
        else:
            sec.end_line = len(lines)

    # Compute TOC bounds (end = the line where the first section 2 starts, or
    # the line before). We want everything from `## Table of Contents` up to
    # but not including the next top-level section (which is "## 2. ...").
    if toc_start >= 0 and len(sections) >= 2:
        # Section 2's start line marks where TOC ends.
        toc_end = sections[1].start_line

    # If TOC falls between section 1 and section 2 (typical), section 1's
    # end_line should actually be toc_start, not the next section. We don't
    # want the TOC to be included in section 1.
    if toc_start >= 0 and sections:
        if sections[0].end_line > toc_start:
            sections[0].end_line = toc_start

    return sections, toc_start, toc_end


def gather_sub_anchors(lines: list[str], sections: list[Section]) -> None:
    """Fill in `sub_anchors` for each section by scanning H3/H4 headings.

    Two keys are written per heading:
    - Dotted numbers like "4.1" or "4.6.1" for numbered subsections. The value
      is the GitHub slug of the full heading line.
    - The heading's GitHub slug itself (for unnumbered H3/H4 anchors like
      "Core Design Principles" → "core-design-principles"). The value matches
      the key — storing it lets us look up the anchor by slug during link
      rewriting without needing a dotted number.
    """
    in_fence = False

    def section_of(i: int) -> Section | None:
        for s in sections:
            if s.start_line <= i < s.end_line:
                return s
        return None

    for i, line in enumerate(lines):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        heading = None
        m = HEADING_H3_RE.match(line)
        if m:
            heading = m.group(1).strip()
        else:
            m = HEADING_H4_RE.match(line)
            if m:
                heading = m.group(1).strip()
        if not heading:
            continue
        sec = section_of(i)
        if sec is None:
            continue
        slug = slugify(heading)
        # Always record the slug so we can retarget unnumbered anchor links.
        sec.sub_anchors[slug] = slug
        # Additionally, record under a dotted number when the heading is
        # numbered (e.g. `### 4.1 …`).
        sm = SUB_SECTION_RE.match(heading)
        if not sm:
            continue
        dotted = sm.group(1)
        # Only record subsections whose leading number matches the owning
        # section (guards against weirdly nested numbers).
        top = dotted.split(".")[0]
        if str(sec.number) != top:
            continue
        sec.sub_anchors[dotted] = slug


def build_anchor_index(sections: list[Section]) -> dict[str, tuple[Section, str | None]]:
    """Map every known anchor slug to (owning_section, anchor_in_file_or_None).

    Anchor in file is None when the anchor IS the top-level heading of the
    file (pointing to the file alone is cleaner than `#<file-slug>`).
    """
    idx: dict[str, tuple[Section, str | None]] = {}
    for sec in sections:
        # Top-level anchor -> link to file with no anchor.
        idx[sec.anchor] = (sec, None)
        # Subsection anchors -> link to file with anchor.
        for dotted, anchor in sec.sub_anchors.items():
            idx[anchor] = (sec, anchor)
    return idx


def build_dotted_index(sections: list[Section]) -> dict[str, tuple[Section, str | None]]:
    """Map dotted numbers ("4", "4.1", "4.6.1") to (section, anchor_or_None)."""
    idx: dict[str, tuple[Section, str | None]] = {}
    for sec in sections:
        idx[str(sec.number)] = (sec, None)
        for dotted, anchor in sec.sub_anchors.items():
            idx[dotted] = (sec, anchor)
    return idx


SECTION_REF_RE = re.compile(r"\bSection (\d+(?:\.\d+)*)\b")
PARA_REF_RE = re.compile(r"§(\d+(?:\.\d+)*)")


def find_free_ranges(line: str) -> list[tuple[int, int]]:
    """Return (start, end) ranges where substitutions are safe.

    Excludes:
    - Inline code spans `...`.
    - Markdown link text `[...]`.
    - Markdown link URLs `(...)` that follow `]`.
    """
    protected: list[tuple[int, int]] = []

    # Inline code spans (single or multiple backticks, non-greedy within run).
    for m in re.finditer(r"(`+)(?:(?!\1)[\s\S])+?\1", line):
        protected.append(m.span())

    # Markdown link text.
    for m in re.finditer(r"\[[^\]]*\]", line):
        protected.append(m.span())

    # Markdown link URL (the `(...)` immediately after `]`).
    for m in re.finditer(r"\]\(([^)]*)\)", line):
        # Protect the parentheses and their contents.
        protected.append((m.start(1) - 1, m.end(1) + 1))

    # Merge and invert.
    if not protected:
        return [(0, len(line))]
    protected.sort()
    merged: list[list[int]] = []
    for a, b in protected:
        if merged and a <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], b)
        else:
            merged.append([a, b])

    free: list[tuple[int, int]] = []
    cursor = 0
    for a, b in merged:
        if cursor < a:
            free.append((cursor, a))
        cursor = max(cursor, b)
    if cursor < len(line):
        free.append((cursor, len(line)))
    return free


def rewrite_line(
    line: str,
    current_section: Section | None,
    anchor_idx: dict[str, tuple[Section, str | None]],
    dotted_idx: dict[str, tuple[Section, str | None]],
) -> str:
    """Rewrite a single line outside code fences.

    - Convert plain-text `Section N(.M)*` refs into markdown links when a
      matching section/subsection exists.
    - Convert `§N(.M)*` refs similarly, unless N looks like a non-section
      citation (e.g. §164.xxx HIPAA refs).
    - Rewrite existing `[text](#anchor)` links to cross-file links when the
      anchor lives in a different section.
    """

    # Step 1: rewrite existing anchor-only markdown links. We do this before
    # plain-text rewrites so we don't match inside already-linked text.
    def _anchor_link_sub(m: re.Match[str]) -> str:
        text = m.group(1)
        anchor = m.group(2)
        hit = anchor_idx.get(anchor)
        if not hit:
            return m.group(0)
        target_sec, target_anchor = hit
        if current_section is not None and target_sec.number == current_section.number:
            # Same file — keep as bare anchor, but prefer anchor-less when
            # linking to the file's H2 heading (the file heading slug is the
            # section's anchor).
            if target_anchor is None:
                # Bare section link inside its own file — point to top anchor.
                return f"[{text}](#{target_sec.anchor})"
            return f"[{text}](#{target_anchor})"
        if target_anchor is None:
            return f"[{text}]({target_sec.file_name})"
        return f"[{text}]({target_sec.file_name}#{target_anchor})"

    line = re.sub(r"\[([^\]]+)\]\(#([^)]+)\)", _anchor_link_sub, line)

    # Step 2: plain-text rewrites only in unprotected ranges.
    free_ranges = find_free_ranges(line)

    # Build a number → Section map for fallback resolution when a specific
    # dotted subsection isn't a heading (e.g. Section 21.1 lives in body text
    # as bold `**21.1 …**` rather than a numbered H3).
    by_number = {
        str(s.number): s
        for _, (s, _) in anchor_idx.items()
    }

    def _resolve_dotted(dotted: str) -> tuple[Section, str | None] | None:
        hit = dotted_idx.get(dotted)
        if hit:
            return hit
        top = dotted.split(".")[0]
        sec = by_number.get(top)
        if sec is not None:
            return (sec, None)
        return None

    def _plain_section_sub(match: re.Match[str]) -> str:
        dotted = match.group(1)
        hit = _resolve_dotted(dotted)
        if not hit:
            return match.group(0)
        target_sec, target_anchor = hit
        label = f"Section {dotted}"
        if current_section is not None and target_sec.number == current_section.number:
            if target_anchor is None:
                return f"[{label}](#{target_sec.anchor})"
            return f"[{label}](#{target_anchor})"
        if target_anchor is None:
            return f"[{label}]({target_sec.file_name})"
        return f"[{label}]({target_sec.file_name}#{target_anchor})"

    # External-spec indicators that mean a `§X` reference points outside
    # Lenny. These must sit directly before the `§` sign (optionally across a
    # comma, "," or "at "), so we don't trip on "spec" used as a generic word
    # earlier in the same sentence.
    external_context_re = re.compile(
        r"(?:\bRFC\s*\d+[A-Z]?|\b\w+\s+spec(?:ification)?|\bA2A\s+(?:spec|protocol)|"
        r"\bMCP\s+spec|\bOAuth\s*2?(?:\.1)?|\bOIDC|\bHTTP/[0-9.]+|\bHIPAA|"
        r"\bGDPR\s+Article|\bCFR|\bUSC)"
        r"(?:[\s,(]*(?:at|in|§)?\s*)$",
        re.IGNORECASE,
    )

    def _is_external_context(text: str, pos: int) -> bool:
        # `pos` is the index of `§` within `text`. Look at the ~40 chars
        # before it; the context regex uses a trailing `$` anchor so only a
        # directly-adjacent external indicator counts.
        window = text[max(0, pos - 40) : pos]
        return external_context_re.search(window) is not None

    def _plain_para_sub(match: re.Match[str]) -> str:
        dotted = match.group(1)
        # Refuse to link citations that clearly aren't Lenny sections. A Lenny
        # section number starts with 1..24; HIPAA/regulatory citations like
        # §164.312 begin with values well outside that range.
        top = dotted.split(".")[0]
        try:
            top_n = int(top)
        except ValueError:
            return match.group(0)
        if not (1 <= top_n <= 24):
            return match.group(0)
        # `match.string` is the segment passed to re.sub, which is what we
        # want — positions are relative to it.
        if _is_external_context(match.string, match.start()):
            return match.group(0)
        hit = _resolve_dotted(dotted)
        if not hit:
            return match.group(0)
        target_sec, target_anchor = hit
        label = f"§{dotted}"
        if current_section is not None and target_sec.number == current_section.number:
            if target_anchor is None:
                return f"[{label}](#{target_sec.anchor})"
            return f"[{label}](#{target_anchor})"
        if target_anchor is None:
            return f"[{label}]({target_sec.file_name})"
        return f"[{label}]({target_sec.file_name}#{target_anchor})"

    # Apply substitutions piecewise so protected ranges remain untouched.
    out = []
    cursor = 0
    for a, b in free_ranges:
        if cursor < a:
            out.append(line[cursor:a])
        segment = line[a:b]
        segment = SECTION_REF_RE.sub(_plain_section_sub, segment)
        segment = PARA_REF_RE.sub(_plain_para_sub, segment)
        out.append(segment)
        cursor = b
    if cursor < len(line):
        out.append(line[cursor:])
    return "".join(out)


def rewrite_block(
    block_lines: list[str],
    current_section: Section | None,
    anchor_idx: dict[str, tuple[Section, str | None]],
    dotted_idx: dict[str, tuple[Section, str | None]],
) -> list[str]:
    """Rewrite a block of lines, skipping fenced code blocks."""
    out: list[str] = []
    in_fence = False
    for line in block_lines:
        if FENCE_RE.match(line):
            in_fence = not in_fence
            out.append(line)
            continue
        if in_fence:
            out.append(line)
            continue
        out.append(rewrite_line(line, current_section, anchor_idx, dotted_idx))
    return out


def compose_readme(
    preamble_lines: list[str],
    toc_lines: list[str],
    sections: list[Section],
    anchor_idx: dict[str, tuple[Section, str | None]],
    dotted_idx: dict[str, tuple[Section, str | None]],
) -> str:
    """Build the README.md that replaces SPEC.md's TOC."""
    # Rewrite preamble/TOC links to point at the per-section files. The
    # preamble is short (title + status + `---`), but it might contain
    # `Section X` references in its executive summary if the summary is
    # retained here. We only emit preamble lines up to (but not including)
    # section 1 — that preserves the title block.
    rewritten_preamble = rewrite_block(preamble_lines, None, anchor_idx, dotted_idx)
    rewritten_toc = rewrite_block(toc_lines, None, anchor_idx, dotted_idx)

    body = []
    body.extend(rewritten_preamble)
    # Separator between preamble and TOC. Avoid stacking `---` if the preamble
    # already ends with one (accounting for trailing blank lines).
    tail = [ln for ln in reversed(rewritten_preamble) if ln.strip() != ""]
    already_separated = bool(tail) and tail[0].strip() == "---"
    if rewritten_preamble and not already_separated:
        body.append("---\n")
        body.append("\n")
    body.extend(rewritten_toc)
    return "".join(body)


def write_section_file(
    dest_dir: Path,
    section: Section,
    content_lines: list[str],
) -> None:
    path = dest_dir / section.file_name
    path.write_text("".join(content_lines), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", default="SPEC.md", help="Input markdown file")
    parser.add_argument("--dest", default="spec", help="Output directory (will be replaced)")
    parser.add_argument(
        "--keep-existing",
        action="store_true",
        help="Skip wiping the destination directory (for debugging)",
    )
    args = parser.parse_args()

    source = Path(args.source)
    dest = Path(args.dest)

    if not source.exists():
        print(f"error: source not found: {source}", file=sys.stderr)
        return 2

    text = source.read_text(encoding="utf-8")
    # Keep trailing newlines on lines so we can reassemble faithfully.
    lines = text.splitlines(keepends=True)

    sections, toc_start, toc_end = parse_sections(lines)
    if not sections:
        print("error: no `## N. Title` headings found", file=sys.stderr)
        return 2

    gather_sub_anchors(lines, sections)
    anchor_idx = build_anchor_index(sections)
    dotted_idx = build_dotted_index(sections)

    if not args.keep_existing and dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True, exist_ok=True)

    # Preamble = everything before the first section's start_line.
    preamble_lines = lines[: sections[0].start_line]

    # TOC block.
    toc_block: list[str] = []
    if toc_start >= 0 and toc_end >= 0:
        toc_block = lines[toc_start:toc_end]

    # Write README.md with preamble + rewritten TOC.
    readme_text = compose_readme(preamble_lines, toc_block, sections, anchor_idx, dotted_idx)
    # Trim trailing separator runs to avoid doubling.
    readme_text = re.sub(r"(?:\n---\s*\n)+\Z", "\n", readme_text)
    # Make sure file ends with a newline.
    if not readme_text.endswith("\n"):
        readme_text += "\n"
    (dest / "README.md").write_text(readme_text, encoding="utf-8")

    # Emit per-section files.
    for section in sections:
        block_lines = lines[section.start_line : section.end_line]
        # Strip a trailing `---\n` separator (and possible blank line) so the
        # file ends cleanly without inheriting the section separator.
        trimmed = block_lines[:]
        while trimmed and trimmed[-1].strip() == "":
            trimmed.pop()
        if trimmed and trimmed[-1].strip() == "---":
            trimmed.pop()
        # Rewrite cross-refs with the current section as context.
        rewritten = rewrite_block(trimmed, section, anchor_idx, dotted_idx)
        # Ensure trailing newline.
        if rewritten and not rewritten[-1].endswith("\n"):
            rewritten[-1] = rewritten[-1] + "\n"
        write_section_file(dest, section, rewritten)

    # Summary.
    print(f"wrote {len(sections)} section files + README.md to {dest}/")
    for s in sections:
        print(f"  {s.file_name}  ({len(s.sub_anchors)} sub-anchors)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
