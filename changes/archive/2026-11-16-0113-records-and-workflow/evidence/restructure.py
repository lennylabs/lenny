#!/usr/bin/env python3
"""Split each change into change.md, decisions.md, and steps.md, and nest the
mechanism sections of change.md under "Detailed design" (step 0113.S5).

Run from the repository root:
  python3 changes/0113-records-and-workflow/evidence/restructure.py plan     # print the classification
  python3 changes/0113-records-and-workflow/evidence/restructure.py apply    # rewrite the folders
  python3 changes/0113-records-and-workflow/evidence/restructure.py links    # compare broken links with HEAD
  python3 changes/0113-records-and-workflow/evidence/restructure.py text <rev>  # every line of text survives

Top-level sections are classified by name. A section named with a label such as
"CT-1 Protocol members" is a mechanism section. Every mechanism section moves,
in its original order, under one "## Detailed design" heading placed where the
first one stood, and each of its headings goes down one level. Links to a
heading that moved to another file are rewritten throughout changes/.
"""
import collections, os, re, subprocess, sys

ROOT = 'changes'
DECISIONS = ['Open decisions', 'Decisions', 'Owner decisions', 'Settled decisions', "Owner's choice",
             "Owner's direction", 'Decision history']
STEPS = ['Validation owed', 'Validation still owed', 'Spikes owed', 'Validation record', 'Dependencies', 'Steps']
HISTORY = ['Capture history']
DESIGN_WRAPPER = ['Design']
MECHANISM = {'External memory pattern', 'What replaces each connector guarantee', 'Git', 'Credential delivery to runtimes',
             'Interfaces', 'Effective policy', 'Injected configuration', 'Runtime SDKs', 'Pod models',
             'Network policy and placement', 'Defaults, replacement, and augmentation', 'TLS interception',
             'Architecture', 'Re-validation through the broker', 'Levers', 'Node-level snapshots',
             "What replaces the cache's guarantees", 'Security effects', 'Billing effects', 'Removals', 'Fields',
             'Who turns it on', 'Identity model (TS-2, TS-4, and TS-6 to TS-9)', 'Configuration by scope',
             'Reserved for other designs', 'Stored formats and wire contracts this forces'}
LABEL = re.compile(r'^([A-Z]{2,4}-[0-9A-Z]+[a-z]?)[:.]? ')
LINK = re.compile(r'(\]\()([^)\s]+)(\))')
CODE = re.compile(r'```.*?```|`[^`\n]*`', re.S)


def change_dirs():
    return [d for d in sorted(os.listdir(ROOT)) if re.match(r'^\d{4}-', d) and os.path.isfile(f'{ROOT}/{d}/change.md')]


def slugify(text):
    t = text.strip().lower().replace('`', '')
    t = re.sub(r'\[([^\]]*)\]\([^)]*\)', r'\1', t)
    t = re.sub(r'[^\w\- ]', '', t)
    return t.replace(' ', '-')


def heading_lines(lines):
    fence = False
    for i, line in enumerate(lines):
        if line.lstrip().startswith('```'):
            fence = not fence
            continue
        if fence:
            continue
        m = re.match(r'^(#{1,6}) (.*?)\s*$', line)
        if m:
            yield i, len(m.group(1)), m.group(2)


def anchors_in(text):
    seen, out = {}, []
    for _, _, title in heading_lines(text.split('\n')):
        title = re.sub(r'\s*\{#[^}]*\}\s*$', '', title)
        s = slugify(title)
        n = seen.get(s, 0)
        out.append(s if n == 0 else f'{s}-{n}')
        seen[s] = n + 1
    return out


def explicit_anchors(text):
    return set(re.findall(r'<a id="([^"]+)"', text)) | set(re.findall(r'\{#([^}]+)\}', text))


def classify(title):
    if title in DECISIONS:
        return 'decisions'
    if title in STEPS:
        return 'steps'
    if title in HISTORY:
        return 'history'
    if title in DESIGN_WRAPPER:
        return 'wrapper'
    if LABEL.match(title) or title in MECHANISM:
        return 'mechanism'
    return 'change'


def split_sections(text):
    """Return (preamble lines, [(title, lines including the heading)])."""
    lines = text.split('\n')
    tops = [(i, t) for i, lvl, t in heading_lines(lines) if lvl == 2]
    if not tops:
        return lines, []
    pre = lines[:tops[0][0]]
    secs = []
    for k, (i, t) in enumerate(tops):
        end = tops[k + 1][0] if k + 1 < len(tops) else len(lines)
        secs.append((t, lines[i:end]))
    return pre, secs


def demote(lines):
    out, fence = [], False
    for line in lines:
        if line.lstrip().startswith('```'):
            fence = not fence
        if not fence and re.match(r'^#{2,5} ', line):
            line = '#' + line
        out.append(line)
    return out


def plan_change(d):
    text = open(f'{ROOT}/{d}/change.md').read()
    pre, secs = split_sections(text)
    title = re.search(r'^# (.*)$', text, re.M).group(1)
    kinds = [(t, classify(t)) for t, _ in secs]
    return text, title, pre, secs, kinds


def build(d):
    text, title, pre, secs, kinds = plan_change(d)
    change, decisions, steps, history = list(pre), [], [], []
    mech = [(t, ls) for (t, ls), (_, k) in zip(secs, kinds) if k == 'mechanism']
    wrapper = [(t, ls) for (t, ls), (_, k) in zip(secs, kinds) if k == 'wrapper']
    placed, interleaved = False, False
    seen_mech = False
    for (t, ls), (_, k) in zip(secs, kinds):
        if k == 'decisions':
            decisions += ls
        elif k == 'steps':
            steps += ls
        elif k == 'history':
            history += ls
        elif k in ('mechanism', 'wrapper'):
            if not placed:
                change.append('## Detailed design')
                change.append('')
                for wt, wls in wrapper:
                    change += wls[1:]
                for mt, mls in mech:
                    change += demote(mls)
                placed = True
            seen_mech = True
        else:
            if seen_mech and placed and k == 'change':
                pass
            change += ls
    # detect interleaving: a change-kind section between the first and last mechanism section
    idx = [i for i, (_, k) in enumerate(kinds) if k in ('mechanism', 'wrapper')]
    if idx:
        interleaved = [kinds[i][0] for i in range(idx[0], idx[-1]) if kinds[i][1] in ('change',)]
    out = {'change.md': '\n'.join(change).rstrip('\n') + '\n'}
    if decisions:
        out['decisions.md'] = f'# Decisions: {d[:4]} {title}\n\n' + '\n'.join(decisions).rstrip('\n') + '\n'
    if steps:
        out['steps.md'] = f'# Steps: {d[:4]} {title}\n\n' + '\n'.join(steps).rstrip('\n') + '\n'
    return text, out, history, interleaved


def anchor_map(old_text, new_files):
    """Map each old anchor of change.md to (file, new anchor), following headings in order."""
    old_lines = old_text.split('\n')
    old_heads = [(re.sub(r'\s*\{#[^}]*\}\s*$', '', t)) for _, _, t in heading_lines(old_lines)]
    old_anchors = anchors_in(old_text)
    pos = collections.defaultdict(list)
    for f, txt in new_files.items():
        heads = [re.sub(r'\s*\{#[^}]*\}\s*$', '', t) for _, _, t in heading_lines(txt.split('\n'))]
        for h, a in zip(heads, anchors_in(txt)):
            pos[h].append((f, a))
    m = {}
    used = collections.Counter()
    for h, a in zip(old_heads, old_anchors):
        cands = pos.get(h, [])
        k = used[h]
        used[h] += 1
        if k < len(cands):
            m[a] = cands[k]
    for f, txt in new_files.items():
        for a in explicit_anchors(txt):
            m[a] = (f, a)
    if 'design' in m and m['design'][0] == 'change.md' and 'detailed-design' in anchors_in(new_files['change.md']):
        pass
    return m


def rewrite(text, fix):
    out, last = [], 0
    for c in CODE.finditer(text):
        out.append(LINK.sub(fix, text[last:c.start()]))
        out.append(c.group(0))
        last = c.end()
    out.append(LINK.sub(fix, text[last:]))
    return ''.join(out)


def apply():
    maps, results = {}, {}
    for d in change_dirs():
        old, out, history, inter = build(d)
        amap = anchor_map(old, out)
        # a "Design" wrapper's own anchor now points at Detailed design
        if any(t == 'Design' for t, _ in split_sections(old)[1]):
            amap['design'] = ('change.md', 'detailed-design')
        maps[f'{ROOT}/{d}/change.md'] = amap
        results[d] = (out, history, inter)
    for d, (out, history, inter) in results.items():
        for f, txt in out.items():
            open(f'{ROOT}/{d}/{f}', 'w').write(txt)
        if history:
            rh = f'{ROOT}/{d}/review-history.md'
            open(rh, 'a').write('\n' + '\n'.join(history).rstrip('\n') + '\n')
        if inter:
            print(f'{d}: sections between mechanism sections now follow Detailed design: {inter}')
    # rewrite links in every markdown file under changes/
    for dp, _, fs in os.walk(ROOT):
        for fn in fs:
            if not fn.endswith('.md'):
                continue
            path = os.path.join(dp, fn)
            text = open(path).read()

            def fix(m, path=path):
                target = m.group(2)
                if re.match(r'^[a-z]+:', target):
                    return m.group(0)
                p, _, frag = target.partition('#')
                if not frag:
                    return m.group(0)
                src_dir = os.path.dirname(path)
                dest = os.path.normpath(os.path.join(src_dir, p)) if p else path
                # a link from a split-out file to its own folder's change.md anchor, or within change.md
                if dest.endswith(('/decisions.md', '/steps.md')) and not p:
                    dest_change = os.path.join(os.path.dirname(dest), 'change.md')
                elif dest.endswith('/change.md'):
                    dest_change = dest
                else:
                    return m.group(0)
                amap = maps.get(dest_change)
                if not amap or frag not in amap:
                    return m.group(0)
                nf, na = amap[frag]
                new_dest = os.path.join(os.path.dirname(dest_change), nf)
                if os.path.normpath(new_dest) == os.path.normpath(path):
                    return f'{m.group(1)}#{na}{m.group(3)}'
                rel = os.path.relpath(new_dest, src_dir)
                return f'{m.group(1)}{rel}#{na}{m.group(3)}'
            out = rewrite(text, fix)
            if out != text:
                open(path, 'w').write(out)


def broken(files_root='.'):
    res = []
    cache = {}
    for dp, _, fs in os.walk(ROOT):
        for fn in fs:
            if not fn.endswith('.md'):
                continue
            path = os.path.join(dp, fn)
            text = CODE.sub('', open(path).read())
            for m in LINK.finditer(text):
                t = m.group(2)
                if re.match(r'^[a-z]+:', t):
                    continue
                p, _, frag = t.partition('#')
                dest = os.path.normpath(os.path.join(dp, p)) if p else path
                if not os.path.exists(dest):
                    res.append((path, t, 'file'))
                    continue
                if frag and dest.endswith('.md'):
                    if dest not in cache:
                        tx = open(dest).read()
                        cache[dest] = set(anchors_in(tx)) | explicit_anchors(tx)
                    if frag not in cache[dest]:
                        res.append((path, t, 'anchor'))
    return res


def norm(path, target):
    folder = re.sub(r'/(change|decisions|steps|review-history)\.md$', '/', path)
    t = re.sub(r'(change|decisions|steps)\.md(?=#|$)', 'X.md', target)
    return folder, t


def main():
    cmd = sys.argv[1]
    if cmd == 'plan':
        for d in change_dirs():
            _, _, _, secs, kinds = plan_change(d)
            c = collections.Counter(k for _, k in kinds)
            print(d, dict(c), [t for t, k in kinds if k in ('wrapper', 'history')])
    elif cmd == 'baseline':
        import json
        json.dump(sorted(set(norm(p, t) for p, t, _ in broken())), open('/dev/stdout', 'w'))
    elif cmd == 'apply':
        apply()
    elif cmd == 'links':
        import json
        base = set(tuple(x) for x in json.load(open(sys.argv[2])))
        now = broken()
        new = [x for x in now if norm(x[0], x[1]) not in base]
        print(f'{len(now)} broken links now, {len(base)} in the baseline; {len(new)} new')
        for x in new[:60]:
            print(*x, sep=' | ')
    elif cmd == 'text':
        rev = sys.argv[2]
        bad = 0
        for d in change_dirs():
            r = subprocess.run(['git', 'show', f'{rev}:{ROOT}/{d}/change.md'], capture_output=True, text=True)
            if r.returncode:
                continue
            rh = subprocess.run(['git', 'show', f'{rev}:{ROOT}/{d}/review-history.md'], capture_output=True, text=True).stdout

            def body(t):
                t = LINK.sub(lambda m: m.group(1) + ')', t)
                return collections.Counter(l.strip() for l in t.split('\n') if l.strip() and not re.match(r'^#{1,6} ', l)
                                           and not l.startswith('Status:'))
            before = body(r.stdout) + body(rh)
            after = collections.Counter()
            for f in ('change.md', 'decisions.md', 'steps.md', 'review-history.md'):
                p = f'{ROOT}/{d}/{f}'
                if os.path.exists(p):
                    after += body(open(p).read())
            lost = before - after
            added = after - before
            if lost or added:
                bad += 1
                print(f'== {d}: {sum(lost.values())} lines lost, {sum(added.values())} added')
                for l in list(lost)[:3]:
                    print('  - ' + l[:140])
                for l in list(added)[:3]:
                    print('  + ' + l[:140])
        print(f'{bad} changes whose text differs')


if __name__ == '__main__':
    main()
