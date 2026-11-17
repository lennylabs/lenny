#!/usr/bin/env python3
"""Check every step entry under changes/ and generate changes/BOARD.md (step 0113.S4).

Run from the repository root:
  python3 changes/0113-records-and-workflow/evidence/steps.py check
  python3 changes/0113-records-and-workflow/evidence/steps.py board
  python3 changes/0113-records-and-workflow/evidence/steps.py unchanged <rev>

unchanged reports each change whose text outside the Steps and Dependencies
sections differs from <rev>, so a conversion can be shown to touch only those.

The changes README defines the step entry. This script stands in for the
`board` helper until 0115 builds it.
"""
import os, re, sys

ROOT = 'changes'
STEP_STATUS = ['Planned', 'Building', 'Landed', 'Cancelled']
CHANGE_STATUS = ['Draft', 'Decided', 'In progress', 'Implemented', 'Cancelled']
LANES = {'specification', 'code', 'documentation', 'non-specification'}
HEAD = re.compile(r'^### (\d{4})\.S(\d+) (.+)$')
FIELD = re.compile(r'^- ([a-z-]+): (.*)$')
STEP_REF = re.compile(r'^\d{4}\.S\d+$')
PART = ' (part)'


def base(ref):
    return ref[:-len(PART)] if ref.endswith(PART) else ref


def change_dirs():
    for d in sorted(os.listdir(ROOT)):
        if re.match(r'^\d{4}-', d) and os.path.isfile(f'{ROOT}/{d}/change.md'):
            yield d


def parse_list(v):
    v = v.strip()
    if not (v.startswith('[') and v.endswith(']')):
        return None
    return [x.strip() for x in v[1:-1].split(',') if x.strip()]


def sections(text):
    out, cur, buf = {}, None, []
    for line in text.split('\n'):
        if line.startswith('## '):
            if cur:
                out[cur] = '\n'.join(buf)
            cur, buf = line[3:].strip(), []
        elif cur:
            buf.append(line)
    if cur:
        out[cur] = '\n'.join(buf)
    return out


def load():
    changes, steps, errors = {}, {}, []
    for d in change_dirs():
        num = d[:4]
        text = open(f'{ROOT}/{d}/change.md').read()
        title = re.search(r'^# (.*)$', text, re.M).group(1)
        m = re.search(r'^Status: `([^`]+)`', text, re.M)
        status = m.group(1) if m else None
        if status not in CHANGE_STATUS:
            errors.append(f'{d}: status line missing or status {status!r} unknown')
        secs = sections(text)
        open_dec = secs.get('Open decisions', '')
        n_open = len(re.findall(r'(?m)^(\d+\.|-) ', open_dec)) if not re.search(r'(?i)^\s*(none|to be framed)', open_dec.strip()) else 0
        changes[num] = {'dir': d, 'title': title, 'status': status, 'open': n_open, 'steps': []}
        lines = text.split('\n')
        in_steps = False
        for i, line in enumerate(lines):
            if line.startswith('## '):
                in_steps = line.strip() == '## Steps'
            hm = HEAD.match(line)
            if not hm:
                if re.match(r'^### \d{4}\.S', line):
                    errors.append(f'{d}: malformed step heading: {line}')
                continue
            sid = f'{hm.group(1)}.S{hm.group(2)}'
            if not in_steps:
                errors.append(f'{d}: step {sid} sits outside the Steps section')
            if hm.group(1) != num:
                errors.append(f'{d}: step {sid} is numbered for another change')
            fields, j = {}, i + 1
            while j < len(lines) and FIELD.match(lines[j]):
                k, v = FIELD.match(lines[j]).groups()
                fields[k] = v
                j += 1
            st = fields.get('status')
            if st not in STEP_STATUS:
                errors.append(f'{sid}: status {st!r} unknown')
            for k in ('aliases', 'lanes', 'depends-on', 'closes'):
                if k in fields and parse_list(fields[k]) is None:
                    errors.append(f'{sid}: field {k} is not a [list]')
            lanes = parse_list(fields.get('lanes', '')) or []
            if not lanes:
                errors.append(f'{sid}: lanes missing')
            for l in lanes:
                if l not in LANES:
                    errors.append(f'{sid}: lane {l!r} unknown')
            for k in fields:
                if k not in ('status', 'aliases', 'lanes', 'depends-on', 'closes'):
                    errors.append(f'{sid}: field {k!r} unknown')
            if sid in steps:
                errors.append(f'{sid}: defined twice')
            steps[sid] = {'change': num, 'title': hm.group(3), 'status': st,
                          'aliases': parse_list(fields.get('aliases', '')) or [],
                          'deps': parse_list(fields.get('depends-on', '')) or [],
                          'closes': parse_list(fields.get('closes', '')) or []}
            changes[num]['steps'].append(sid)
    return changes, steps, errors


def check(changes, steps, errors):
    for num, c in changes.items():
        ns = [int(s.split('.S')[1]) for s in c['steps']]
        if ns != list(range(1, len(ns) + 1)):
            errors.append(f'{c["dir"]}: steps are not numbered S1 to S{len(ns)} in order: {ns}')
        sts = [steps[s]['status'] for s in c['steps']]
        started = any(s in ('Building', 'Landed') for s in sts)
        done = sts and all(s in ('Landed', 'Cancelled') for s in sts)
        if c['status'] in ('Draft', 'Decided') and started:
            errors.append(f'{c["dir"]}: status {c["status"]} but a step has started')
        if c['status'] == 'In progress' and not started:
            errors.append(f'{c["dir"]}: status In progress but no step has started')
        if c['status'] == 'Implemented' and not done:
            errors.append(f'{c["dir"]}: status Implemented but a step is open')
    alias_owner = {}
    for sid, s in steps.items():
        for a in s['aliases']:
            if a.lower().startswith('proposal ') or a.endswith('(part)'):
                continue
            if a in alias_owner and alias_owner[a] != sid:
                errors.append(f'alias {a} names both {alias_owner[a]} and {sid}')
            alias_owner[a] = sid
        for dep in s['deps']:
            dep = base(dep)
            if not STEP_REF.match(dep):
                errors.append(f'{sid}: dependency {dep!r} is not a step identifier')
            elif dep not in steps:
                errors.append(f'{sid}: dependency {dep} does not exist')
            elif dep == sid:
                errors.append(f'{sid}: depends on itself')
    state, cycles = {}, []

    def visit(n, path):
        state[n] = 1
        for d in steps[n]['deps']:
            if d.endswith(PART) or d not in steps:
                continue
            if state.get(d) == 1:
                cycles.append(path[path.index(d):] + [d] if d in path else [n, d])
            elif not state.get(d):
                visit(d, path + [d])
        state[n] = 2
    sys.setrecursionlimit(10000)
    for n in steps:
        if not state.get(n):
            visit(n, [n])
    for c in cycles:
        errors.append('dependency cycle: ' + ' -> '.join(c))
    return errors


def program(path):
    text = open(path).read()
    fm = text.split('---')[1]
    ms, cur = [], None
    for line in fm.split('\n'):
        m = re.match(r'^  - id: (.*)$', line)
        if m:
            cur = {'id': m.group(1).strip(), 'title': '', 'refs': []}
            ms.append(cur)
            continue
        if cur is None:
            continue
        m = re.match(r'^    title: (.*)$', line)
        if m:
            cur['title'] = m.group(1).strip()
        m = re.match(r'^    steps: \[(.*)\]$', line)
        if m:
            cur['refs'] = [x.strip().strip('"') for x in m.group(1).split(',') if x.strip()]
    cm = re.search(r'^current: (.*)$', fm, re.M)
    return ms, (cm.group(1).strip() if cm else None)


def expand(ref, changes, steps):
    if ref in steps:
        return [ref]
    if re.fullmatch(r'\d{4}', ref) and ref in changes:
        return list(changes[ref]['steps'])
    return []


def board(changes, steps):
    ms, current = program(f'{ROOT}/programs/v1.md')
    placed, rows = set(), []

    def link(sid):
        c = changes[steps[sid]['change']]
        h = f'{sid} {steps[sid]["title"]}'.lower().replace('`', '')
        h = re.sub(r'[^\w\- ]', '', h).replace(' ', '-')
        return f'[{sid}]({c["dir"]}/change.md#{h})'

    def classify(sid):
        s = steps[sid]
        if s['status'] in ('Landed', 'Cancelled', 'Building'):
            return s['status']
        open_deps = [d for d in s['deps'] if d in steps and steps[d]['status'] not in ('Landed', 'Cancelled')]  # parts never block
        if open_deps:
            return 'Blocked'
        if changes[s['change']]['status'] == 'Draft':
            return 'Waiting on decisions'
        return 'Ready'

    order = ['Building', 'Ready', 'Waiting on decisions', 'Blocked', 'Landed', 'Cancelled']

    def table(sids):
        out = ['| Step | Title | State | Waiting on |', '| :-- | :-- | :-- | :-- |']
        for sid in sorted(sids, key=lambda x: (order.index(classify(x)), x)):
            s = steps[sid]
            st = classify(sid)
            wait = ''
            if st == 'Blocked':
                wait = ', '.join(d for d in s['deps'] if d in steps and steps[d]['status'] not in ('Landed', 'Cancelled'))
            parts = [d for d in s['deps'] if d.endswith(PART) and steps.get(base(d), {}).get('status') not in ('Landed', 'Cancelled')]
            if parts:
                wait = (wait + '; ' if wait else '') + 'a part waits on ' + ', '.join(base(d) for d in parts)
            elif st == 'Waiting on decisions':
                wait = f'{s["change"]} is `Draft`'
            out.append(f'| {link(sid)} | {s["title"]} | {st} | {wait} |')
        return out

    out = ['# Board', '',
           'Generated by `changes/0113-records-and-workflow/evidence/steps.py board` from the step entries of every change and from the [v1 program](programs/v1.md). Do not edit it by hand.', '',
           'A step is ready when every step it depends on has landed and its change is not `Draft`. A step of a `Draft` change waits on that change\'s decisions, unless the owner rules that none of them touches it.', '']
    counts = {}
    for sid in steps:
        counts[classify(sid)] = counts.get(classify(sid), 0) + 1
    out.append('## Summary')
    out.append('')
    out.append('| State | Steps |')
    out.append('| :-- | --: |')
    for k in order:
        if counts.get(k):
            out.append(f'| {k} | {counts[k]} |')
    out.append('')
    for m in ms:
        sids, notes = [], []
        for r in m['refs']:
            e = expand(r, changes, steps)
            if e:
                sids += e
            else:
                notes.append(r)
        sids = [s for s in dict.fromkeys(sids) if s not in placed]
        placed.update(sids)
        out.append(f'## {m["id"]}: {m["title"]}' + (' (current)' if m['id'] == current else ''))
        out.append('')
        if notes:
            out.append('Not yet resolved to steps: ' + '; '.join(notes) + '.')
            out.append('')
        if sids:
            out += table(sids)
            out.append('')
    rest = [s for s in steps if s not in placed]
    if rest:
        out.append('## Not yet in a milestone')
        out.append('')
        out += table(rest)
        out.append('')
    out.append('## Changes')
    out.append('')
    out.append('| Change | Title | Status | Steps landed | Open decisions |')
    out.append('| :-- | :-- | :-- | :-- | --: |')
    for num, c in changes.items():
        n = len(c['steps'])
        landed = sum(1 for s in c['steps'] if steps[s]['status'] == 'Landed')
        out.append(f'| [{num}]({c["dir"]}/change.md) | {c["title"]} | `{c["status"]}` | {landed} of {n} | {c["open"]} |')
    out.append('')
    open(f'{ROOT}/BOARD.md', 'w').write('\n'.join(out))


def strip_sections(text):
    out, skip = [], False
    for line in text.split('\n'):
        if line.startswith('## '):
            skip = line.strip() in ('## Steps', '## Dependencies')
        if not skip:
            out.append(line)
    return '\n'.join(out).rstrip('\n')


def unchanged(rev):
    import subprocess, difflib
    bad = 0
    for d in change_dirs():
        p = f'{ROOT}/{d}/change.md'
        r = subprocess.run(['git', 'show', f'{rev}:{p}'], capture_output=True, text=True)
        if r.returncode:
            continue
        a, b = strip_sections(r.stdout), strip_sections(open(p).read())
        if a != b:
            bad += 1
            print(f'== {d}')
            for l in list(difflib.unified_diff(a.split('\n'), b.split('\n'), lineterm='', n=0))[2:12]:
                print('  ' + l[:160])
    print(f'{bad} changes differ outside Steps and Dependencies')
    return bad


def main():
    if sys.argv[1] == 'unchanged':
        sys.exit(1 if unchanged(sys.argv[2]) else 0)
    changes, steps, errors = load()
    errors = check(changes, steps, errors)
    if sys.argv[1] == 'check':
        for e in errors:
            print(e)
        print(f'{len(steps)} steps in {len(changes)} changes; {len(errors)} problems')
        sys.exit(1 if errors else 0)
    if sys.argv[1] == 'board':
        board(changes, steps)
        print(f'BOARD.md written; {len(errors)} problems (run check)')


if __name__ == '__main__':
    main()
