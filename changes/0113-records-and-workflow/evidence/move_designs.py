#!/usr/bin/env python3
"""Move designs/ into changes/ for change 0113 (step 0113.S2).

Run from the repository root:
  python3 changes/0113-records-and-workflow/evidence/move_designs.py check-before
  python3 changes/0113-records-and-workflow/evidence/move_designs.py move
  python3 changes/0113-records-and-workflow/evidence/move_designs.py check-after

check-before records the broken links that exist before the move, keyed by the
moved file and the link's target after rewriting, so check-after reports only
links the move broke.
"""
import json, os, re, subprocess, sys

HERE = 'changes/0113-records-and-workflow/evidence'
BASELINE = os.path.join(HERE, 'broken-links-before.json')
DATE = '2026-11-16'
REVIEW_HEADINGS = {'Review history', 'Revision history'}
PLAN_HEADINGS = {'Proposal split and order', 'Proposal split', 'Proposals', 'Proposals and dependencies'}
LINK = re.compile(r'(\]\()([^)\s]+)(\))')
CODE = re.compile(r'```.*?```|`[^`\n]*`', re.S)

designs = sorted(f for f in os.listdir('designs') if f.endswith('-design.md')) if os.path.isdir('designs') else []
NUM = {f[:-len('-design.md')]: f'{118 + i:04d}-{f[:-len("-design.md")]}' for i, f in enumerate(designs)}


def slugify(text):
    t = text.strip().lower().replace('`', '')
    t = re.sub(r'\[([^\]]*)\]\([^)]*\)', r'\1', t)
    t = re.sub(r'[^\w\- ]', '', t)
    return t.replace(' ', '-')


def headings(text):
    fence = False
    for i, line in enumerate(text.split('\n')):
        if line.lstrip().startswith('```'):
            fence = not fence
            continue
        if fence:
            continue
        m = re.match(r'^(#{1,6}) (.*?)\s*(\{#[^}]*\})?\s*$', line)
        if m:
            yield i, len(m.group(1)), m.group(2)


def anchors(text):
    seen, out = {}, set()
    for _, _, title in headings(text):
        s = slugify(title)
        n = seen.get(s, 0)
        out.add(s if n == 0 else f'{s}-{n}')
        seen[s] = n + 1
    out.update(re.findall(r'<a id="([^"]+)"', text))
    out.update(re.findall(r'\{#([^}]+)\}', text))
    return out


def rewrite_prose(text):
    def ev(m):
        slug = m.group(1)
        if slug == 'spec-structure':
            return 'changes/0115-spec-structure/evidence/'
        return f'changes/{NUM[slug]}/evidence/' if slug in NUM else m.group(0)
    text = re.sub(r'designs/evidence/([a-z0-9-]+)-design/', ev, text)
    text = re.sub(r'designs/([a-z0-9-]+)-design\.md', lambda m: f'changes/{NUM[m.group(1)]}/change.md' if m.group(1) in NUM else m.group(0), text)
    text = re.sub(r'(?<![\w/.-])([a-z0-9-]+)-design\.md', lambda m: NUM.get(m.group(1), m.group(0)), text)
    return text


def map_path(p):
    m = re.fullmatch(r'designs/([a-z0-9-]+)-design\.md', p)
    if m and m.group(1) in NUM:
        return f'changes/{NUM[m.group(1)]}/change.md'
    if p == 'designs/evidence/README.md':
        return f'{HERE}/designs-evidence-README.md'
    m = re.match(r'designs/evidence/([a-z0-9-]+)-design(/.*)?$', p)
    if m:
        slug, rest = m.group(1), m.group(2) or ''
        if slug == 'spec-structure':
            return 'changes/0115-spec-structure/evidence' + rest
        if slug in NUM:
            return f'changes/{NUM[slug]}/evidence' + rest
    if p in ('designs', 'designs/evidence'):
        return 'changes'
    return p


def split_design(text):
    """Return (change text, review lines or None, review anchors, renamed anchors)."""
    lines = text.split('\n')
    tops = [(i, t) for i, lvl, t in headings(text) if lvl == 2]
    renamed = {}
    for _, _, t in headings(text):
        new = rewrite_prose(t)
        if slugify(new) != slugify(t):
            renamed[slugify(t)] = slugify(new)
    review = None
    for k, (i, t) in enumerate(tops):
        end = tops[k + 1][0] if k + 1 < len(tops) else len(lines)
        if t in REVIEW_HEADINGS and review is None:
            review = (i, end)
        if t in PLAN_HEADINGS:
            renamed[slugify(t)] = 'steps'
            lines[i] = '## Steps'
    body, review_anchors = None, set()
    if review:
        i, end = review
        review_anchors = anchors('\n'.join(lines[i:end]))
        body = [('#' + l[3:] if l.startswith('### ') else ('##' + l[4:] if l.startswith('#### ') else l)) for l in lines[i + 1:end]]
        del lines[i:end]
    return '\n'.join(lines), body, review_anchors, renamed


def rewrite_links(text, old_file, new_file, kind, own, info):
    """kind: change, review, or other. own and info[old design path]: (review anchors, renamed anchors)."""
    def fix(m):
        target = m.group(2)
        if re.match(r'^[a-z]+:', target):
            return m.group(0)
        path, _, frag = target.partition('#')
        if not path:
            ra, ren = own
            frag2 = ren.get(frag, frag)
            if kind == 'change' and frag in ra:
                return f'{m.group(1)}review-history.md#{frag2}{m.group(3)}'
            if kind == 'review' and frag not in ra:
                return f'{m.group(1)}change.md#{frag2}{m.group(3)}'
            return f'{m.group(1)}#{frag2}{m.group(3)}'
        old_abs = os.path.normpath(os.path.join(os.path.dirname(old_file), path))
        new_abs = map_path(old_abs)
        if new_abs == old_abs and not os.path.exists(old_abs):
            return m.group(0)  # already broken before the move; leave it as written
        if old_abs in info and frag:
            ra, ren = info[old_abs]
            if frag in ra:
                new_abs = new_abs.replace('/change.md', '/review-history.md')
            frag = ren.get(frag, frag)
        rel = os.path.relpath(new_abs, os.path.dirname(new_file))
        if path.endswith('/') and not rel.endswith('/'):
            rel += '/'
        return f'{m.group(1)}{rel}{"#" + frag if frag else ""}{m.group(3)}'
    out, last = [], 0
    for c in CODE.finditer(text):
        out.append(LINK.sub(fix, text[last:c.start()]))
        out.append(c.group(0))
        last = c.end()
    out.append(LINK.sub(fix, text[last:]))
    return ''.join(out)


def broken_links(files):
    out, cache = [], {}
    for f in files:
        text = CODE.sub('', open(f).read())
        for m in LINK.finditer(text):
            target = m.group(2)
            if re.match(r'^[a-z]+:', target):
                continue
            path, _, frag = target.partition('#')
            dest = os.path.normpath(os.path.join(os.path.dirname(f), path)) if path else f
            if not os.path.exists(dest):
                out.append((f, target, 'missing file'))
                continue
            if frag and dest.endswith('.md'):
                if dest not in cache:
                    cache[dest] = anchors(open(dest).read())
                if frag not in cache[dest]:
                    out.append((f, target, 'missing anchor'))
    return out


def md_files(top):
    for d, _, fs in os.walk(top):
        for f in fs:
            if f.endswith('.md'):
                yield os.path.join(d, f)


def git(*args):
    subprocess.run(['git', *args], check=True)


def status_line(text):
    lines = text.split('\n')
    for i, line in enumerate(lines[:6]):
        if line.startswith('Status: design for owner review, '):
            lines[i] = 'Status: `Draft`, ' + line[len('Status: design for owner review, '):]
            lines.insert(i + 1, '\nPrograms: [v1](../programs/v1.md). The owner\'s final cross-review of the moved designs is pending, so the status is `Draft`.')
            break
    return '\n'.join(lines)


def move():
    info, texts = {}, {}
    for f in designs:
        old = f'designs/{f}'
        change, review, ra, ren = split_design(open(old).read())
        info[old] = (ra, ren)
        texts[old] = (change, review)
    moves = {}
    for f in designs:
        slug = f[:-len('-design.md')]
        dest = f'changes/{NUM[slug]}'
        os.makedirs(dest, exist_ok=True)
        git('mv', f'designs/{f}', f'{dest}/change.md')
        moves[f'{dest}/change.md'] = f'designs/{f}'
        if os.path.isdir(f'designs/evidence/{slug}-design'):
            git('mv', f'designs/evidence/{slug}-design', f'{dest}/evidence')
    git('mv', 'designs/evidence/spec-structure-design', 'changes/0115-spec-structure/evidence')
    git('mv', 'designs/evidence/README.md', f'{HERE}/designs-evidence-README.md')
    for f in md_files('changes'):
        if f in moves:
            continue
        old = f
        if f.startswith('changes/0115-spec-structure/evidence/'):
            old = 'designs/evidence/spec-structure-design/' + f[len('changes/0115-spec-structure/evidence/'):]
        elif f == f'{HERE}/designs-evidence-README.md':
            old = 'designs/evidence/README.md'
        else:
            for slug, n in NUM.items():
                p = f'changes/{n}/evidence/'
                if f.startswith(p):
                    old = f'designs/evidence/{slug}-design/' + f[len(p):]
        moves[f] = old
    for new, old in moves.items():
        if old in texts:
            change, review = texts[old]
            ra, ren = info[old]
            slug = old[len('designs/'):-len('-design.md')]
            change = rewrite_prose(rewrite_links(change, old, new, 'change', (ra, ren), info))
            change = status_line(change)
            open(new, 'w').write(change)
            title = re.search(r'^# (.*)$', change, re.M).group(1)
            rh = f'{os.path.dirname(new)}/review-history.md'
            body = '\n'.join(review) if review else ''
            body = rewrite_prose(rewrite_links(body, old, rh, 'review', (ra, ren), info))
            entry = (f'\n\n## Move into changes ({DATE})\n\nChange 0113 moved this design from `designs/{slug}-design.md` '
                     f'into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence '
                     f'moved into `evidence/`, its status became `Draft` pending the owner\'s final cross-review, and every path to '
                     f'a design or its evidence, in links and in prose, now names its new location.\n')
            open(rh, 'w').write(f'# Review history: {NUM[slug]} {title}\n\n' + body.strip('\n') + entry)
        else:
            text = open(new).read()
            out = rewrite_prose(rewrite_links(text, old, new, 'other', (set(), {}), info))
            if out != text:
                open(new, 'w').write(out)
    for extra in ['changes/0113-records-and-workflow/change.md', 'changes/programs/v1.md', 'changes/README.md',
                  'changes/0114-helpers/change.md', 'changes/0115-spec-structure/change.md']:
        text = open(extra).read()
        out = rewrite_prose(rewrite_links(text, extra, extra, 'other', (set(), {}), info))
        if out != text:
            open(extra, 'w').write(out)
    json.dump(NUM, open(os.path.join(HERE, 'numbering.json'), 'w'), indent=1)


def main():
    cmd = sys.argv[1]
    if cmd == 'check-before':
        b = broken_links(list(md_files('designs')))
        json.dump(sorted({(map_path(f), t) for f, t, _ in b}), open(BASELINE, 'w'), indent=1)
        print(f'{len(b)} broken links before the move')
    elif cmd == 'move':
        move()
    elif cmd == 'check-after':
        b = broken_links(list(md_files('changes')))
        base = {(f, t) for f, t in json.load(open(BASELINE))}
        new = [x for x in b if (x[0], x[1]) not in base]
        print(f'{len(b)} broken links after the move; {len(new)} not in the baseline')
        for x in new[:80]:
            print(*x, sep=' | ')


if __name__ == '__main__':
    main()
