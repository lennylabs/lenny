#!/usr/bin/env python3
"""Reference tool prototype for the migrated specification under spec-next/.

The tool builds a registry from the pages themselves and keeps a snapshot of
it in tools/registry.json, so that a later run can tell where a page or an
identifier used to live.

Registry sources:
  - front matter `code:` (the page code) and optional `aliases: [..]`;
  - explicit anchors `<a id="..."></a>` in a page body. These are the
    identifier anchors: requirements (claim-r4), owned terms (reserved-hold),
    operations (podclaim-create), and definition blocks (setting-...).
Heading attributes `{#...}` are local anchors. A link from another page may
not target one.

Commands:
  check          verify every relative link under spec-next/ resolves, and
                 that each cross-page fragment is a registered identifier on
                 the target page; writes the registry snapshot when clean.
  fix            rewrite link paths after pages move (git mv), using the
                 registry snapshot to map an old path to its code and an
                 identifier to the page that now holds it; then re-snapshot.
  where ID       print the owning page of a code, identifier, or operation
                 and every `spec: ID` site under pkg/, cmd/, and tests/.
  registry       print the registry.

Python 3 standard library only.
"""
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # spec-next
REPO = os.path.dirname(ROOT)
SNAPSHOT = os.path.join(ROOT, "tools", "registry.json")

FM_RE = re.compile(r"\A---\n(.*?)\n---\n", re.S)
ANCHOR_RE = re.compile(r'<a id="([^"]+)"></a>')
HEADING_ATTR_RE = re.compile(r"^#{1,6} .*\{#([A-Za-z0-9_.-]+)\}\s*$", re.M)
LINK_RE = re.compile(r"(?<!!)\[([^\]]*)\]\(([^)\s]+)\)")
FENCE_RE = re.compile(r"^```.*?^```", re.S | re.M)
COMMENT_RE = re.compile(r"<!--.*?-->", re.S)
CODE_SPAN_RE = re.compile(r"`[^`\n]*`")
OPERATION_RE = re.compile(r"^([a-z0-9-]+)#([a-z0-9-]+)\.([a-zA-Z0-9]+)$")


def md_files():
    out = []
    for d, _, fs in os.walk(ROOT):
        if os.path.join(ROOT, "tools") in d:
            continue
        for f in fs:
            if f.endswith(".md"):
                out.append(os.path.relpath(os.path.join(d, f), ROOT))
    return sorted(out)


def read(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as fh:
        return fh.read()


def front_matter(text):
    m = FM_RE.match(text)
    fm = {}
    if not m:
        return fm
    for line in m.group(1).splitlines():
        mm = re.match(r"^([a-z-]+):\s*(.*)$", line)
        if mm:
            key, val = mm.group(1), mm.group(2).strip()
            if val.startswith("[") and val.endswith("]"):
                val = [v.strip() for v in val[1:-1].split(",") if v.strip()]
            fm[key] = val
    return fm


def strip_inert(text):
    """Remove fenced blocks and HTML comments, keeping line structure."""
    blank = lambda m: re.sub(r"[^\n]", " ", m.group(0))
    return CODE_SPAN_RE.sub(blank, COMMENT_RE.sub(blank, FENCE_RE.sub(blank, text)))


def build_registry():
    reg = {"codes": {}, "aliases": {}, "anchors": {}, "paths": {}, "dups": []}
    for rel in md_files():
        text = read(rel)
        fm = front_matter(text)
        code = fm.get("code")
        if code:
            if code in reg["codes"]:
                reg["dups"].append(("code", code, reg["codes"][code], rel))
            reg["codes"][code] = rel
            reg["paths"][rel] = code
            for al in fm.get("aliases", []) or []:
                reg["aliases"][al] = code
        for a in ANCHOR_RE.findall(strip_inert(text)):
            if a in reg["anchors"] and reg["anchors"][a] != rel:
                reg["dups"].append(("anchor", a, reg["anchors"][a], rel))
            reg["anchors"][a] = rel
    return reg


def local_anchors(text):
    t = strip_inert(text)
    return set(ANCHOR_RE.findall(t)) | set(HEADING_ATTR_RE.findall(t))


def iter_links(rel, text):
    """Yield (match, target, fragment) for relative links outside fences and comments."""
    inert = strip_inert(text)
    for m in LINK_RE.finditer(inert):
        url = m.group(2)
        if re.match(r"^[a-z]+:", url) or url.startswith("/"):
            continue
        path, _, frag = url.partition("#")
        yield m, path, frag


def check(verbose=True):
    reg = build_registry()
    errors, checked = [], 0
    for code, a, b in [(d[1], d[2], d[3]) for d in reg["dups"]]:
        errors.append(f"duplicate registration {code}: {a} and {b}")
    anchor_cache = {}
    for rel in md_files():
        text = read(rel)
        for m, path, frag in iter_links(rel, text):
            checked += 1
            here = os.path.dirname(rel)
            if path == "":
                target_rel = rel
            else:
                target_abs = os.path.normpath(os.path.join(ROOT, here, path))
                if not os.path.exists(target_abs):
                    errors.append(f"{rel}: broken path {path}")
                    continue
                target_rel = os.path.relpath(target_abs, ROOT)
            if not frag:
                continue
            if target_rel.startswith(".."):
                continue  # outside spec-next: path existence only
            if target_rel == rel:
                if target_rel not in anchor_cache:
                    anchor_cache[target_rel] = local_anchors(text)
                if frag not in anchor_cache[target_rel]:
                    errors.append(f"{rel}: missing local anchor #{frag}")
                continue
            owner = reg["anchors"].get(frag)
            if owner is None:
                errors.append(f"{rel}: #{frag} on {target_rel} is not a registered identifier")
            elif owner != target_rel:
                errors.append(f"{rel}: #{frag} points at {target_rel} but is registered on {owner}")
    if verbose:
        print(f"pages: {len(md_files())}; codes: {len(reg['codes'])}; identifiers: {len(reg['anchors'])}")
        print(f"links checked: {checked}; errors: {len(errors)}")
        for e in errors:
            print("  " + e)
    if not errors:
        save_snapshot(reg)
    return errors, checked


def save_snapshot(reg):
    snap = {"codes": reg["codes"], "aliases": reg["aliases"], "anchors": reg["anchors"]}
    with open(SNAPSHOT, "w", encoding="utf-8") as fh:
        json.dump(snap, fh, indent=1, sort_keys=True)
        fh.write("\n")


def load_snapshot():
    if not os.path.exists(SNAPSHOT):
        return {"codes": {}, "aliases": {}, "anchors": {}}
    with open(SNAPSHOT, encoding="utf-8") as fh:
        return json.load(fh)


def fix():
    old = load_snapshot()
    old_path_code = {p: c for c, p in old["codes"].items()}
    reg = build_registry()
    rewritten, files_changed, unresolved = 0, 0, []
    for rel in md_files():
        text = read(rel)
        here = os.path.dirname(rel)
        # Where was this file before the move? Its code tells us.
        code = reg["paths"].get(rel)
        old_rel = old["codes"].get(code, rel) if code else rel
        old_here = os.path.dirname(old_rel)
        edits = []
        for m, path, frag in iter_links(rel, text):
            if path == "":
                continue
            new_target = None
            # Resolve the link as it was written, from the file's old location.
            old_target = os.path.normpath(os.path.join(old_here, path))
            cur_target = os.path.normpath(os.path.join(here, path))
            if frag and frag in reg["anchors"]:
                new_target = reg["anchors"][frag]
            elif old_target in old_path_code:
                c = old_path_code[old_target]
                c = reg["aliases"].get(c, c)
                new_target = reg["codes"].get(c)
            elif os.path.exists(os.path.join(ROOT, cur_target)):
                continue
            elif os.path.exists(os.path.join(ROOT, old_target)):
                new_target = old_target
            if new_target is None:
                unresolved.append(f"{rel}: {path}#{frag}")
                continue
            if cur_target == new_target:
                continue  # the link as written still reaches the right page
            new_path = os.path.relpath(os.path.join(ROOT, new_target), os.path.join(ROOT, here))
            if new_path != path:
                url = new_path + ("#" + frag if frag else "")
                edits.append((m.start(2), m.end(2), url))
        if edits:
            for s, e, url in sorted(edits, reverse=True):
                text = text[:s] + url + text[e:]
            with open(os.path.join(ROOT, rel), "w", encoding="utf-8") as fh:
                fh.write(text)
            rewritten += len(edits)
            files_changed += 1
    print(f"links rewritten: {rewritten} in {files_changed} files")
    for u in unresolved:
        print("  unresolved: " + u)
    errors, _ = check(verbose=False)
    if errors:
        print(f"check after fix: {len(errors)} errors")
        for e in errors:
            print("  " + e)
    return rewritten


def where(ident):
    reg = build_registry()
    owner = None
    m = OPERATION_RE.match(ident)
    if ident in reg["codes"]:
        owner = reg["codes"][ident]
    elif ident in reg["aliases"]:
        owner = reg["codes"].get(reg["aliases"][ident])
    elif m:
        owner = reg["anchors"].get(f"{m.group(2)}-{m.group(3).lower()}")
    else:
        owner = reg["anchors"].get(ident.lower())
    print(f"{ident}: {('spec-next/' + owner) if owner else 'not registered'}")
    pat = re.compile(r"spec:.*(?<![A-Za-z0-9-])" + re.escape(ident) + r"(?![A-Za-z0-9])")
    sites = 0
    for top in ("pkg", "cmd", "tests"):
        base = os.path.join(REPO, top)
        for d, _, fs in os.walk(base):
            for f in fs:
                if not f.endswith(".go"):
                    continue
                p = os.path.join(d, f)
                try:
                    with open(p, encoding="utf-8") as fh:
                        for i, line in enumerate(fh, 1):
                            if pat.search(line):
                                print(f"  {os.path.relpath(p, REPO)}:{i}: {line.strip()[:120]}")
                                sites += 1
                except (UnicodeDecodeError, OSError):
                    continue
    print(f"code sites: {sites}")


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    cmd = argv[1]
    if cmd == "check":
        errors, _ = check()
        return 1 if errors else 0
    if cmd == "fix":
        fix()
        return 0
    if cmd == "where" and len(argv) == 3:
        where(argv[2])
        return 0
    if cmd == "registry":
        print(json.dumps(build_registry(), indent=1, sort_keys=True))
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
