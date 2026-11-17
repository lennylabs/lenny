#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Generate the folder index pages of the specification tree.

Run from the repository root:
  python3 scripts/specindex/specindex.py           # write every index.md
  python3 scripts/specindex/specindex.py --check   # exit 1 when one is stale

The input is the code registry in spec/conventions.md, the table under the
heading "Code registry" with one row per page: code, page type, path relative
to spec/, and summary. Every folder of the tree has a row whose path is
<folder>/index.md. The index page of a folder is written from that row and
from the rows of the folder's own pages and of its subfolders' index pages:

  front matter (code, type: index, area under capabilities/<area>/, summary,
  and status: placeholder while the index carries a "Content today" section),
  the page title, the summary as a paragraph, and the table of pages.

The title is read from the existing index page and is derived from the folder
name when the page does not exist yet. Every section of the existing page
after the table, such as "Content today", is kept as written, so an index that
owns content of its own keeps its pointers while the generator rewrites the
listing. The title of each listed page is the first level-one heading of that
page.

The specification describes this layout in spec/conventions.md. This is
contributor tooling rather than a specified behavior of Lenny, so it cites no
requirement.
"""
import argparse
import os
import re
import sys

REGISTRY_HEADING = "## Code registry"
PAGES_HEADING = "## Pages"
ROW = re.compile(r"^\|\s*`?([A-Z]{2,9})`?\s*\|\s*([a-z-]+)\s*\|\s*\[`?([^`\]]+)`?\]\([^)]*\)\s*\|\s*(.*?)\s*\|\s*$")
H1 = re.compile(r"^#\s+(.+?)\s*$")


class RegistryError(Exception):
    """The code registry is missing, malformed, or leaves a folder without an index row."""


def read_registry(spec_dir):
    """Return the registry rows as (code, type, path, summary) tuples, in file order."""
    path = os.path.join(spec_dir, "conventions.md")
    try:
        with open(path, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except OSError as err:
        raise RegistryError("read %s: %s" % (path, err))
    try:
        start = lines.index(REGISTRY_HEADING)
    except ValueError:
        raise RegistryError("%s has no %r section" % (path, REGISTRY_HEADING))
    rows = []
    for line in lines[start + 1:]:
        if line.startswith("## "):
            break
        if not line.startswith("|") or set(line.replace("|", "").strip()) <= set(":- "):
            continue
        m = ROW.match(line)
        if m is None:
            if line.lstrip("| ").lower().startswith("code"):
                continue
            raise RegistryError("malformed code registry row: %s" % line)
        code, typ, rel, summary = m.groups()
        rows.append((code, typ, rel, summary.replace("\\|", "|")))
    if not rows:
        raise RegistryError("the code registry in %s has no row" % path)
    return rows


def first_heading(path):
    """Return the first level-one heading of a page, or None."""
    try:
        with open(path, encoding="utf-8") as fh:
            in_front = False
            for i, line in enumerate(fh):
                line = line.rstrip("\n")
                if i == 0 and line == "---":
                    in_front = True
                    continue
                if in_front:
                    if line == "---":
                        in_front = False
                    continue
                m = H1.match(line)
                if m:
                    return m.group(1)
    except OSError:
        return None
    return None


def kept_sections(path):
    """Return the text of the existing page from its first level-two heading other than Pages."""
    try:
        with open(path, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except OSError:
        return ""
    for i, line in enumerate(lines):
        if line.startswith("## ") and line != PAGES_HEADING:
            return "\n".join(lines[i:]).rstrip("\n") + "\n"
    return ""


def folder_title(folder):
    name = folder.rsplit("/", 1)[-1].replace("-", " ")
    return name[:1].upper() + name[1:]


def render(spec_dir, folder, row, entries):
    """Render one index page."""
    code, _type, path, summary = row
    index_path = os.path.join(spec_dir, path)
    title = first_heading(index_path) or folder_title(folder)
    tail = kept_sections(index_path)
    out = ["---", "code: %s" % code, "type: index"]
    parts = folder.split("/")
    if parts[0] == "capabilities" and len(parts) > 1:
        out.append("area: %s" % parts[1])
    out += ["summary: >-", "  " + summary]
    if "## Content today" in tail:
        out.append("status: placeholder")
    out += ["---", "", "# %s" % title, "", summary, "", PAGES_HEADING, ""]
    out += ["| Page | Code | Summary |", "| :-- | :-- | :-- |"]
    for e_code, _e_type, e_path, e_summary in entries:
        rel = os.path.relpath(e_path, folder)
        e_title = first_heading(os.path.join(spec_dir, e_path))
        if e_title is None:
            e_title = folder_title(os.path.dirname(e_path)) if e_path.endswith("/index.md") else rel
        out.append("| [%s](%s) | `%s` | %s |" % (e_title, rel, e_code, e_summary.replace("|", "\\|")))
    text = "\n".join(out) + "\n"
    if tail:
        text += "\n" + tail
    return text


def plan(spec_dir):
    """Return {index path relative to spec/: content} for every folder of the registry."""
    rows = read_registry(spec_dir)
    codes = {}
    for r in rows:
        if r[0] in codes:
            raise RegistryError("code %s is registered twice" % r[0])
        codes[r[0]] = r
    index_rows = {os.path.dirname(r[2]): r for r in rows if os.path.basename(r[2]) == "index.md"}
    folders = set()
    for r in rows:
        d = os.path.dirname(r[2])
        while d:
            folders.add(d)
            d = os.path.dirname(d)
    missing = sorted(f for f in folders if f not in index_rows)
    if missing:
        raise RegistryError("folder(s) with no index row in the code registry: %s" % ", ".join(missing))
    out = {}
    for folder, row in sorted(index_rows.items()):
        pages, subfolders = [], []
        for r in rows:
            if r is row:
                continue
            d = os.path.dirname(r[2])
            if d == folder:
                pages.append(r)
            elif os.path.basename(r[2]) == "index.md" and os.path.dirname(d) == folder:
                subfolders.append(r)
        pages.sort(key=lambda r: r[2])
        subfolders.sort(key=lambda r: r[2])
        out[row[2]] = render(spec_dir, folder, row, pages + subfolders)
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--root", default=".", help="repository root (default: the working directory)")
    ap.add_argument("--check", action="store_true", help="report stale index pages and write nothing")
    args = ap.parse_args(argv)
    spec_dir = os.path.join(args.root, "spec")
    try:
        pages = plan(spec_dir)
    except RegistryError as err:
        print("specindex: %s" % err, file=sys.stderr)
        return 2
    stale = []
    for rel, content in pages.items():
        full = os.path.join(spec_dir, rel)
        try:
            with open(full, encoding="utf-8") as fh:
                current = fh.read()
        except OSError:
            current = None
        if current == content:
            continue
        stale.append(rel)
        if not args.check:
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "w", encoding="utf-8") as fh:
                fh.write(content)
    if args.check and stale:
        print("specindex: %d index page(s) are stale; run python3 scripts/specindex/specindex.py:" % len(stale), file=sys.stderr)
        for rel in stale:
            print("  spec/%s" % rel, file=sys.stderr)
        return 1
    if not args.check:
        print("specindex: wrote %d of %d index page(s)" % (len(stale), len(pages)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
