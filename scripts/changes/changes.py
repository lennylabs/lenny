#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Commands over the changes/ records: the `change` and `board` helpers, and the
generated section of docs/about/status.md.

Run from the repository root:
  python3 scripts/changes/changes.py new <slug> [<title>]
  python3 scripts/changes/changes.py status <number> <status>
  python3 scripts/changes/changes.py step <step> <status>
  python3 scripts/changes/changes.py archive <number>
  python3 scripts/changes/changes.py check
  python3 scripts/changes/changes.py board [--check]
  python3 scripts/changes/changes.py links [--baseline <file>] [--write-baseline <file>]
  python3 scripts/changes/changes.py status-page [--check]

changes/README.md defines the records these commands read and write. Every
command takes --root <dir> to run against another tree, which the tests use.
"""
import argparse
import datetime
import json
import os
import re
import subprocess
import sys

STEP_STATUS = ['Planned', 'Building', 'Landed', 'Cancelled']
CHANGE_STATUS = ['Draft', 'Decided', 'In progress', 'Implemented', 'Cancelled']
LANES = {'specification', 'code', 'documentation', 'non-specification'}
FIELDS = ('status', 'aliases', 'lanes', 'depends-on', 'closes', 'ruled-ready')
HEAD = re.compile(r'^### (\d{4})\.S(\d+) (.+)$')
FIELD = re.compile(r'^- ([a-z-]+): (.*)$')
STEP_REF = re.compile(r'^\d{4}\.S\d+$')
STATUS_LINE = re.compile(r'(?m)^Status: `([^`]+)`, revision (\d+), (\d{4}-\d{2}-\d{2})\.')
PART = ' (part)'
LINK = re.compile(r'(\]\()([^)\s]+)(\))')
CODE = re.compile(r'```.*?```|`[^`\n]*`', re.S)


class Error(Exception):
    pass


def base(ref):
    return ref[:-len(PART)] if ref.endswith(PART) else ref


# ---------------------------------------------------------------- loading

class Tree:
    def __init__(self, root='.'):
        self.root = root
        self.changes_dir = os.path.join(root, 'changes')

    def path(self, *p):
        return os.path.join(self.changes_dir, *p)

    def checked(self, p):
        """Return p when a write to it stays inside the repository: p is not
        a symbolic link, and its parent directory resolves inside the root.
        Every write and move of the commands goes through this check, so a
        link planted in the tree cannot redirect a write outside it."""
        shown = os.path.relpath(p, self.root)
        if os.path.islink(p):
            raise Error(f'{shown} is a symbolic link; refusing to write through it')
        root = os.path.realpath(self.root)
        parent = os.path.realpath(os.path.dirname(os.path.abspath(p)))
        if parent != root and not parent.startswith(root + os.sep):
            raise Error(f'the directory of {shown} resolves to {parent}, outside the repository; refusing to write')
        return p

    def write(self, p, text):
        """Write text to p after the check of `checked`."""
        write(self.checked(p), text)

    def change_dirs(self, archived=True):
        out = []
        for d in sorted(os.listdir(self.changes_dir)):
            if re.match(r'^\d{4}-', d) and os.path.isfile(self.path(d, 'change.md')):
                out.append(d)
        arch = self.path('archive')
        if archived and os.path.isdir(arch):
            for d in sorted(os.listdir(arch)):
                m = re.match(r'^\d{4}-\d{2}-\d{2}-(\d{4}-.+)$', d)
                if m and os.path.isfile(os.path.join(arch, d, 'change.md')):
                    out.append(os.path.join('archive', d))
        return out

    def find(self, number):
        for d in self.change_dirs():
            name = os.path.basename(d)
            name = re.sub(r'^\d{4}-\d{2}-\d{2}-', '', name)
            if name.startswith(number + '-'):
                return d
        raise Error(f'no change numbered {number}')


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


# A Markdown table separator row: cells of dashes with optional alignment
# colons, with or without a closing pipe.
TABLE_SEPARATOR = re.compile(r'^\|(\s*:?-+:?\s*\|)*\s*:?-+:?\s*\|?\s*$')


def count_open_decisions(text):
    """Count the open decisions in the text of a decisions.md "Open decisions"
    section. A decision is written as a list item (a numbered item or a bullet
    at the start of a line) or as a data row of a Markdown table. A table's
    header row, the row directly above its separator row, and the separator row
    are not decisions, and neither is a line inside a fenced code block. A
    section that opens with "None" or "To be framed" holds none."""
    text = text.strip()
    if not text or re.match(r'(?i)^(none|to be framed)', text):
        return 0
    lines, fenced = [], False
    for line in text.split('\n'):
        if line.lstrip().startswith('```'):
            fenced = not fenced
            continue
        if not fenced:
            lines.append(line)
    n = 0
    for i, line in enumerate(lines):
        if re.match(r'^(\d+\.|-) ', line):
            n += 1
            continue
        if not line.startswith('|') or TABLE_SEPARATOR.match(line):
            continue
        if i + 1 < len(lines) and TABLE_SEPARATOR.match(lines[i + 1]):
            continue
        n += 1
    return n


def read(p):
    with open(p) as f:
        return f.read()


def write(p, text):
    with open(p, 'w') as f:
        f.write(text)


def load(tree):
    changes, steps, errors = {}, {}, []
    for d in tree.change_dirs():
        name = re.sub(r'^\d{4}-\d{2}-\d{2}-', '', os.path.basename(d))
        num = name[:4]
        text = read(tree.path(d, 'change.md'))
        tm = re.search(r'^# (.*)$', text, re.M)
        title = tm.group(1) if tm else name
        m = STATUS_LINE.search(text)
        status = m.group(1) if m else None
        if status not in CHANGE_STATUS:
            errors.append(f'{d}: status line missing or status {status!r} unknown')
        if re.search(r'(?m)^### \d{4}\.S', text):
            errors.append(f'{d}: change.md holds step entries; they belong in steps.md')
        dp = tree.path(d, 'decisions.md')
        secs = sections(read(dp)) if os.path.exists(dp) else {}
        n_open = count_open_decisions(secs.get('Open decisions', ''))
        changes[num] = {'dir': d, 'title': title, 'status': status, 'open': n_open, 'steps': [],
                        'archived': d.startswith('archive')}
        sp = tree.path(d, 'steps.md')
        lines = read(sp).split('\n') if os.path.exists(sp) else []
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
                fields[k] = (v, j)
                j += 1
            st = fields.get('status', (None,))[0]
            if st not in STEP_STATUS:
                errors.append(f'{sid}: status {st!r} unknown')
            if 'ruled-ready' in fields and not re.match(r'^\d{4}-\d{2}-\d{2}\b', fields['ruled-ready'][0]):
                errors.append(f'{sid}: ruled-ready must start with the date of the owner\'s ruling')
            for k in ('aliases', 'lanes', 'depends-on', 'closes'):
                if k in fields and parse_list(fields[k][0]) is None:
                    errors.append(f'{sid}: field {k} is not a [list]')
            lanes = parse_list(fields.get('lanes', ('',))[0]) or []
            if not lanes:
                errors.append(f'{sid}: lanes missing')
            for lane in lanes:
                if lane not in LANES:
                    errors.append(f'{sid}: lane {lane!r} unknown')
            for k in fields:
                if k not in FIELDS:
                    errors.append(f'{sid}: field {k!r} unknown')
            if sid in steps:
                errors.append(f'{sid}: defined twice')
            steps[sid] = {'change': num, 'title': hm.group(3), 'status': st, 'file': sp,
                          'status_line': fields.get('status', (None, None))[1],
                          'aliases': parse_list(fields.get('aliases', ('',))[0]) or [],
                          'deps': parse_list(fields.get('depends-on', ('',))[0]) or [],
                          'closes': parse_list(fields.get('closes', ('',))[0]) or [],
                          'ruled_ready': fields.get('ruled-ready', (None,))[0]}
            changes[num]['steps'].append(sid)
    return changes, steps, errors


# ---------------------------------------------------------------- checking

def status_problem(status, step_states):
    started = any(s in ('Building', 'Landed') for s in step_states)
    done = bool(step_states) and all(s in ('Landed', 'Cancelled') for s in step_states)
    if status in ('Draft', 'Decided') and started:
        return f'status {status} but a step has started'
    if status == 'In progress' and not started:
        return 'status In progress but no step has started'
    if status == 'Implemented' and not done:
        return 'status Implemented but a step is open'
    return None


def check(changes, steps, errors):
    errors = list(errors)
    for num, c in changes.items():
        ns = [int(s.split('.S')[1]) for s in c['steps']]
        if ns != list(range(1, len(ns) + 1)):
            errors.append(f'{c["dir"]}: steps are not numbered S1 to S{len(ns)} in order: {ns}')
        ruled = [s for s in c['steps'] if steps[s].get('ruled_ready')]
        others = [steps[s]['status'] for s in c['steps'] if s not in ruled]
        p = status_problem(c['status'], [steps[s]['status'] for s in c['steps']])
        if p and c['status'] == 'Draft' and ruled and not status_problem('Draft', others):
            p = None  # only steps the owner ruled ready have started
        if p:
            errors.append(f'{c["dir"]}: {p}')
    owner = {}
    for sid, s in steps.items():
        for a in s['aliases']:
            if a.lower().startswith('proposal ') or a.endswith(PART):
                continue
            if a in owner and owner[a] != sid:
                errors.append(f'alias {a} names both {owner[a]} and {sid}')
            owner[a] = sid
        for dep in s['deps']:
            d = base(dep)
            if not STEP_REF.match(d):
                errors.append(f'{sid}: dependency {dep!r} is not a step identifier')
            elif d not in steps:
                errors.append(f'{sid}: dependency {d} does not exist')
            elif d == sid:
                errors.append(f'{sid}: depends on itself')
    for comp in components(steps, partial=False):
        errors.append('dependency cycle among: ' + ', '.join(comp))
    return errors


def components(steps, partial):
    """Strongly connected components of more than one step. With partial=False
    only whole-step edges count; with partial=True every edge does."""
    graph = {n: [base(d) for d in s['deps'] if base(d) in steps and (partial or not d.endswith(PART))]
             for n, s in steps.items()}
    index, low, stack, on, out, counter = {}, {}, [], set(), [], [0]
    sys.setrecursionlimit(max(10000, sys.getrecursionlimit()))

    def strong(v):
        index[v] = low[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on.add(v)
        for w in graph[v]:
            if w not in index:
                strong(w)
                low[v] = min(low[v], low[w])
            elif w in on:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            comp = []
            while True:
                w = stack.pop()
                on.discard(w)
                comp.append(w)
                if w == v:
                    break
            if len(comp) > 1 or v in graph[v]:
                out.append(sorted(comp))
    for v in sorted(graph):
        if v not in index:
            strong(v)
    return out


def warnings(steps):
    whole = {tuple(c) for c in components(steps, partial=False)}
    return ['steps that wait on each other through partial dependencies: ' + ', '.join(c)
            for c in components(steps, partial=True) if tuple(c) not in whole]


# ---------------------------------------------------------------- board

def scalar(lines, i, indent):
    """The value of the key on lines[i]: an inline value, or a folded (`>`, `>-`)
    or literal (`|`, `|-`) block whose lines are indented deeper than `indent`.
    Returns the value and the index of the next line to read."""
    v = lines[i].split(':', 1)[1].strip()
    if v not in ('>', '>-', '|', '|-'):
        return v.strip('"'), i + 1
    j, body = i + 1, []
    while j < len(lines) and (not lines[j].strip() or len(lines[j]) - len(lines[j].lstrip()) > indent):
        body.append(lines[j].strip())
        j += 1
    sep = ' ' if v.startswith('>') else '\n'
    return sep.join(x for x in body if x), j


def programs(tree):
    pd = tree.path('programs')
    out = []
    if not os.path.isdir(pd):
        return out
    for fn in sorted(os.listdir(pd)):
        if not fn.endswith('.md'):
            continue
        text = read(os.path.join(pd, fn))
        if not text.startswith('---'):
            continue
        fm = text.split('---')[1]
        ms, cur, goal = [], None, ''
        lines = fm.split('\n')
        i = 0
        while i < len(lines):
            line = lines[i]
            if line.startswith('goal:'):
                goal, i = scalar(lines, i, 0)
                continue
            m = re.match(r'^  - id: (.*)$', line)
            if m:
                cur = {'id': m.group(1).strip(), 'title': '', 'refs': [], 'exit': ''}
                ms.append(cur)
                i += 1
                continue
            if cur is not None:
                m = re.match(r'^    title: (.*)$', line)
                if m:
                    cur['title'] = m.group(1).strip()
                m = re.match(r'^    steps: \[(.*)\]$', line)
                if m:
                    cur['refs'] = [x.strip().strip('"') for x in m.group(1).split(',') if x.strip()]
                if line.startswith('    exit:'):
                    cur['exit'], i = scalar(lines, i, 4)
                    continue
            i += 1
        cm = re.search(r'^current: (.*)$', fm, re.M)
        pm = re.search(r'^program: (.*)$', fm, re.M)
        out.append({'name': pm.group(1).strip() if pm else fn[:-3], 'file': fn, 'milestones': ms,
                    'current': cm.group(1).strip() if cm else None, 'goal': goal})
    return out


def slug(text):
    t = text.strip().lower().replace('`', '')
    t = re.sub(r'\[([^\]]*)\]\([^)]*\)', r'\1', t)
    t = re.sub(r'[^\w\- ]', '', t)
    return t.replace(' ', '-')


def render_board(tree, changes, steps):
    done = ('Landed', 'Cancelled')

    def classify(sid):
        s = steps[sid]
        if s['status'] in done or s['status'] == 'Building':
            return s['status']
        if any(d in steps and steps[d]['status'] not in done for d in s['deps']):
            return 'Blocked'
        if changes[s['change']]['status'] == 'Draft' and not s['ruled_ready']:
            return 'Waiting on decisions'
        return 'Ready'

    order = ['Building', 'Ready', 'Waiting on decisions', 'Blocked', 'Landed', 'Cancelled']

    def link(sid):
        c = changes[steps[sid]['change']]
        return f'[{sid}]({c["dir"]}/steps.md#{slug(sid + " " + steps[sid]["title"])})'

    def table(sids):
        out = ['| Step | Title | State | Waiting on |', '| :-- | :-- | :-- | :-- |']
        for sid in sorted(sids, key=lambda x: (order.index(classify(x)), x)):
            s, st = steps[sid], classify(sid)
            wait = ''
            if st == 'Blocked':
                wait = ', '.join(d for d in s['deps'] if d in steps and steps[d]['status'] not in done)
            elif st == 'Waiting on decisions':
                wait = f'{s["change"]} is `Draft`'
            parts = [base(d) for d in s['deps'] if d.endswith(PART) and steps.get(base(d), {}).get('status') not in done]
            if parts and st not in done:
                wait = (wait + '; ' if wait else '') + 'a part waits on ' + ', '.join(parts)
            out.append(f'| {link(sid)} | {s["title"]} | {st} | {wait} |')
        return out

    active = [s for s in steps if not changes[steps[s]['change']]['archived']]
    out = ['# Board', '',
           'Generated by `python3 scripts/changes/changes.py board` from the step entries of every change and from the programs under `programs/`. Do not edit it by hand.', '',
           'A step is ready when every step it depends on has landed and its change is not `Draft`. A step of a `Draft` change waits on that change\'s decisions, unless the owner rules that none of them touches it. Archived changes are left out.', '',
           '## Summary', '', '| State | Steps |', '| :-- | --: |']
    counts = {}
    for sid in active:
        counts[classify(sid)] = counts.get(classify(sid), 0) + 1
    out += [f'| {k} | {counts[k]} |' for k in order if counts.get(k)]
    out.append('')
    placed = set()
    for prog in programs(tree):
        out += [f'## Program {prog["name"]}', '']
        for m in prog['milestones']:
            sids, notes = [], []
            for r in m['refs']:
                if r in steps:
                    sids.append(r)
                elif re.fullmatch(r'\d{4}', r) and r in changes and changes[r]['steps']:
                    sids += changes[r]['steps']
                else:
                    notes.append(r)
            sids = [s for s in dict.fromkeys(sids) if s not in placed and s in active]
            placed.update(sids)
            out += [f'### {m["id"]}: {m["title"]}' + (' (current)' if m['id'] == prog['current'] else ''), '']
            if notes:
                out += ['Not yet resolved to steps: ' + '; '.join(notes) + '.', '']
            if sids:
                out += table(sids) + ['']
    rest = [s for s in active if s not in placed]
    if rest:
        out += ['## Not yet in a milestone', ''] + table(rest) + ['']
    out += ['## Changes', '', '| Change | Title | Status | Steps landed | Open decisions |',
            '| :-- | :-- | :-- | :-- | --: |']
    for num, c in sorted(changes.items()):
        if c['archived']:
            continue
        landed = sum(1 for s in c['steps'] if steps[s]['status'] == 'Landed')
        out.append(f'| [{num}]({c["dir"]}/change.md) | {c["title"]} | `{c["status"]}` | {landed} of {len(c["steps"])} | {c["open"]} |')
    out.append('')
    return '\n'.join(out)


# ---------------------------------------------------------------- status page

STATUS_PAGE = os.path.join('docs', 'about', 'status.md')
STATUS_BEGIN = '<!-- generated:status -->'
STATUS_END = '<!-- /generated:status -->'
REPO_URL = 'https://github.com/lennylabs/lenny/blob/main/'
# A parenthesized list of change numbers or step identifiers, which readers of
# the documentation do not see.
CHANGE_REFS = re.compile(r'\s*\((?:\d{4}(?:\.S\d+)?(?:,\s*|\s+and\s+)?)+\)')
CHANGE_NUMBER = re.compile(r'\b(\d{4})(\.S\d+)?\b')


def for_readers(text, changes):
    """Program text as a reader of the documentation sees it: a parenthesized
    list of change numbers is dropped, and a change number or a step identifier
    left in a sentence becomes the change's title. A four-digit number that names
    no change, such as a year, stays."""
    text = CHANGE_REFS.sub('', text)

    def title(m):
        c = changes.get(m.group(1))
        if not c:
            return m.group(0)
        name = f'the "{c["title"]}" change'
        return f'a step of {name}' if m.group(2) else name
    return CHANGE_NUMBER.sub(title, text)


def milestone_steps(m, changes, steps, placed):
    """The steps a milestone's references resolve to, each placed in the first
    milestone that names it, and whether some reference names no step yet."""
    sids, unresolved = [], False
    for r in m['refs']:
        if r in steps:
            sids.append(r)
        elif re.fullmatch(r'\d{4}', r) and r in changes and changes[r]['steps']:
            sids += changes[r]['steps']
        else:
            unresolved = True
    sids = [s for s in dict.fromkeys(sids) if s not in placed]
    placed.update(sids)
    return sids, unresolved


def render_status(tree, changes, steps):
    """The generated section of docs/about/status.md: each program's goal and
    its milestones with their exit criteria and the share of steps landed."""
    out = [STATUS_BEGIN,
           '<!-- Generated by `python3 scripts/changes/changes.py status-page` from the programs under '
           'changes/programs/ and the step entries of each change. Edit the programs, not this section. -->', '']
    for prog in programs(tree):
        out += [f'## The {prog["name"]} program', '']
        if prog['goal']:
            out += [f'Goal: {for_readers(prog["goal"], changes)}', '']
        out += ['The work toward this goal is planned as milestones. A milestone is done when its exit '
                'criteria hold, and its work lands in steps that are each merged on their own. The '
                f'[program]({REPO_URL}changes/programs/{prog["file"]}) states the plan, and the '
                f'[board]({REPO_URL}changes/BOARD.md) lists the steps that are ready to start, building, and blocked.', '',
                '| Milestone | Steps landed | State |', '|:----------|:-------------|:------|']
        placed, rows, details = set(), [], []
        for m in prog['milestones']:
            sids, unresolved = milestone_steps(m, changes, steps, placed)
            counted = [s for s in sids if steps[s]['status'] != 'Cancelled']
            landed = sum(1 for s in counted if steps[s]['status'] == 'Landed')
            building = sum(1 for s in counted if steps[s]['status'] == 'Building')
            if counted and landed == len(counted) and not unresolved:
                state = 'Done'
            elif landed or building:
                state = 'In progress'
            else:
                state = 'Not started'
            heading = for_readers(m['title'], changes)
            title = heading + (' (current)' if m['id'] == prog['current'] else '')
            share = f'{landed} of {len(counted)}' if counted else 'None assigned yet'
            rows.append(f'| {title} | {share} | {state} |')
            details += [f'### {heading}', '']
            if m['exit']:
                details += [f'Exit criteria: {for_readers(m["exit"], changes)}', '']
            if not counted:
                line = 'No steps are assigned to this milestone yet.'
            elif landed == len(counted):
                line = f'All {len(counted)} of its steps have landed.' if len(counted) > 1 else 'Its one step has landed.'
            elif len(counted) == 1:
                line = 'Its one step is being built.' if building else 'Its one step has not started.'
            else:
                line = (f'{landed} of its {len(counted)} steps have landed, and {building} '
                        f'{"is" if building == 1 else "are"} being built.')
            if unresolved and counted:
                line += ' Part of its work is not yet divided into steps, so the count covers only the steps listed.'
            details += [line, '']
        out += rows + [''] + details
    while out and out[-1] == '':
        out.pop()
    out += ['', STATUS_END]
    return '\n'.join(out)


def status_page(tree, changes, steps):
    """The status page with its generated section replaced. The text outside
    the markers is written by hand and kept as it is."""
    path = os.path.join(tree.root, STATUS_PAGE)
    if not os.path.exists(path):
        raise Error(f'{STATUS_PAGE} does not exist')
    text = read(path)
    b, e = text.find(STATUS_BEGIN), text.find(STATUS_END)
    if b < 0 or e < b:
        raise Error(f'{STATUS_PAGE} has no {STATUS_BEGIN} ... {STATUS_END} section to generate')
    return text[:b] + render_status(tree, changes, steps) + text[e + len(STATUS_END):]


# ---------------------------------------------------------------- edits

TEMPLATE = {
    'change.md': """# {title}

Status: `Draft`, revision 1, {date}.

## Problem

## Scope

## Terms

| Term | Meaning |
| :-- | :-- |

## High-level design

## Requirements

## Current state

## Detailed design

## Specification and non-specification changes

## Documentation

## Tests
""",
    'decisions.md': """# Decisions: {number} {title}

## Open decisions

## Decisions
""",
    'steps.md': """# Steps: {number} {title}

## Validation owed

## Steps
""",
    'review-history.md': """# Review history: {number} {title}

- {date}, revision 1. Created.
""",
}


def next_number(tree):
    nums = [0]
    for d in tree.change_dirs():
        name = re.sub(r'^\d{4}-\d{2}-\d{2}-', '', os.path.basename(d))
        nums.append(int(name[:4]))
    for prog in programs(tree):
        text = read(tree.path('programs', prog['file']))
        m = re.search(r'^members: \[(.*)\]$', text, re.M)
        if m:
            for x in m.group(1).split(','):
                for n in re.findall(r'\d{4}', x):
                    nums.append(int(n))
    pdir = os.path.join(tree.root, 'proposals')
    if os.path.isdir(pdir):
        for d in os.listdir(pdir):
            m = re.match(r'^(\d{4})', d)
            if m:
                nums.append(int(m.group(1)))
    return f'{max(nums) + 1:04d}'


def cmd_new(tree, slug_, title, today):
    if not re.fullmatch(r'[a-z0-9]+(-[a-z0-9]+)*', slug_):
        raise Error(f'slug {slug_!r} is not lowercase words joined by hyphens')
    number = next_number(tree)
    d = tree.checked(tree.path(f'{number}-{slug_}'))
    os.makedirs(d)
    title = title or slug_.replace('-', ' ').capitalize()
    for fn, t in TEMPLATE.items():
        tree.write(os.path.join(d, fn), t.format(title=title, number=number, date=today))
    return f'{number}-{slug_}'


def cmd_status(tree, number, status, today):
    if status not in CHANGE_STATUS:
        raise Error(f'status {status!r} is not one of {CHANGE_STATUS}')
    changes, steps, _ = load(tree)
    if number not in changes:
        raise Error(f'no change numbered {number}')
    c = changes[number]
    p = status_problem(status, [steps[s]['status'] for s in c['steps']])
    if p:
        raise Error(f'{number}: {p}')
    path = tree.path(c['dir'], 'change.md')
    text = read(path)
    m = STATUS_LINE.search(text)
    text = text[:m.start()] + f'Status: `{status}`, revision {m.group(2)}, {today}.' + text[m.end():]
    tree.write(path, text)


def cmd_step(tree, sid, status):
    if status not in STEP_STATUS:
        raise Error(f'status {status!r} is not one of {STEP_STATUS}')
    changes, steps, _ = load(tree)
    if sid not in steps:
        raise Error(f'no step {sid}')
    s = steps[sid]
    lines = read(s['file']).split('\n')
    lines[s['status_line']] = f'- status: {status}'
    tree.write(s['file'], '\n'.join(lines))
    c = changes[s['change']]
    states = [status if x == sid else steps[x]['status'] for x in c['steps']]
    p = status_problem(c['status'], states)
    others = [st for x, st in zip(c['steps'], states) if not steps[x].get('ruled_ready')]
    if p and c['status'] == 'Draft' and not status_problem('Draft', others):
        p = None
    return f'{c["dir"]}: change status `{c["status"]}` now disagrees with its steps ({p}); set it with `status`' if p else None


# ---------------------------------------------------------------- links

def anchors(text):
    seen, out, fence = {}, set(), False
    for line in text.split('\n'):
        if line.lstrip().startswith('```'):
            fence = not fence
            continue
        if fence:
            continue
        m = re.match(r'^#{1,6} (.*?)\s*(\{#([^}]*)\})?\s*$', line)
        if m:
            s = slug(m.group(1))
            n = seen.get(s, 0)
            out.add(s if n == 0 else f'{s}-{n}')
            seen[s] = n + 1
            if m.group(3):
                out.add(m.group(3))
    out.update(re.findall(r'<a id="([^"]+)"', text))
    return out


def md_files(tree, top='changes'):
    for d, _, fs in os.walk(os.path.join(tree.root, top)):
        for f in fs:
            if f.endswith('.md'):
                yield os.path.join(d, f)


def broken_links(tree, files):
    out, cache = [], {}
    for f in files:
        text = CODE.sub('', read(f))
        for m in LINK.finditer(text):
            target = m.group(2)
            if re.match(r'^[a-z]+:', target):
                continue
            path, _, frag = target.partition('#')
            dest = os.path.normpath(os.path.join(os.path.dirname(f), path)) if path else f
            if not os.path.exists(dest):
                out.append((os.path.relpath(f, tree.root), target, 'file'))
                continue
            if frag and dest.endswith('.md'):
                if dest not in cache:
                    cache[dest] = anchors(read(dest))
                if frag not in cache[dest]:
                    out.append((os.path.relpath(f, tree.root), target, 'anchor'))
    return out


def rewrite_links(text, fix):
    out, last = [], 0
    for c in CODE.finditer(text):
        out.append(LINK.sub(fix, text[last:c.start()]))
        out.append(c.group(0))
        last = c.end()
    out.append(LINK.sub(fix, text[last:]))
    return ''.join(out)


def tracked_md(tree):
    try:
        r = subprocess.run(['git', 'ls-files', '*.md'], cwd=tree.root,
                           capture_output=True, text=True, check=True)
        return [os.path.join(tree.root, p) for p in r.stdout.split('\n') if p]
    except (OSError, subprocess.CalledProcessError):
        return list(md_files(tree, '.'))


def move_folder(tree, old_rel, new_rel):
    """Move a folder under changes/ and rewrite every relative link into it,
    out of it, and within it."""
    old_abs = os.path.normpath(tree.path(old_rel))
    new_abs = os.path.normpath(tree.path(new_rel))

    def mapped(p):
        p = os.path.normpath(p)
        if p == old_abs or p.startswith(old_abs + os.sep):
            return new_abs + p[len(old_abs):]
        return p
    files = tracked_md(tree)
    moving = [f for f in files if os.path.normpath(f).startswith(old_abs + os.sep)]
    staying = [f for f in files if f not in moving and os.path.exists(f)]

    def fixer(src_old, src_new):
        def fix(m):
            target = m.group(2)
            if re.match(r'^[a-z]+:', target) or target.startswith('#'):
                return m.group(0)
            path, sep, frag = target.partition('#')
            dest = os.path.normpath(os.path.join(os.path.dirname(src_old), path))
            new_dest = mapped(dest)
            if new_dest == dest and src_old == src_new:
                return m.group(0)
            rel = os.path.relpath(new_dest, os.path.dirname(src_new))
            if path.endswith('/') and not rel.endswith('/'):
                rel += '/'
            return f'{m.group(1)}{rel}{sep}{frag}{m.group(3)}'
        return fix
    tree.checked(old_abs)
    tree.checked(new_abs)
    for f in staying:
        text = read(f)
        out = rewrite_links(text, fixer(f, f))
        if out != text:
            tree.write(f, out)
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    moved = subprocess.run(['git', 'mv', old_abs, new_abs], cwd=tree.root, capture_output=True)
    if moved.returncode:
        os.rename(old_abs, new_abs)
    for f in moving:
        nf = mapped(f)
        text = read(nf)
        out = rewrite_links(text, fixer(f, nf))
        if out != text:
            tree.write(nf, out)


def cmd_archive(tree, number, today):
    changes, _, _ = load(tree)
    if number not in changes:
        raise Error(f'no change numbered {number}')
    c = changes[number]
    if c['archived']:
        raise Error(f'{number} is already archived')
    if c['status'] not in ('Implemented', 'Cancelled'):
        raise Error(f'{number} is `{c["status"]}`; only an Implemented or Cancelled change is archived')
    new_rel = os.path.join('archive', f'{today}-{c["dir"]}')
    move_folder(tree, c['dir'], new_rel)
    return new_rel


# ---------------------------------------------------------------- main

def main(argv=None):
    ap = argparse.ArgumentParser(prog='changes.py')
    ap.add_argument('--root', default='.')
    ap.add_argument('--today', default=datetime.date.today().isoformat())
    sub = ap.add_subparsers(dest='cmd', required=True)
    p = sub.add_parser('new'); p.add_argument('slug'); p.add_argument('title', nargs='?')
    p = sub.add_parser('status'); p.add_argument('number'); p.add_argument('status')
    p = sub.add_parser('step'); p.add_argument('step'); p.add_argument('status')
    p = sub.add_parser('archive'); p.add_argument('number')
    sub.add_parser('check')
    p = sub.add_parser('board'); p.add_argument('--check', action='store_true')
    p = sub.add_parser('links'); p.add_argument('--baseline'); p.add_argument('--write-baseline')
    p = sub.add_parser('status-page'); p.add_argument('--check', action='store_true')
    a = ap.parse_args(argv)
    tree = Tree(a.root)
    try:
        if a.cmd == 'new':
            print(cmd_new(tree, a.slug, a.title, a.today))
        elif a.cmd == 'status':
            cmd_status(tree, a.number, a.status, a.today)
        elif a.cmd == 'step':
            note = cmd_step(tree, a.step, a.status)
            if note:
                print(note)
        elif a.cmd == 'archive':
            print(cmd_archive(tree, a.number, a.today))
        elif a.cmd == 'check':
            changes, steps, errors = load(tree)
            errors = check(changes, steps, errors)
            for e in errors:
                print(e)
            for w in warnings(steps):
                print('warning: ' + w)
            print(f'{len(steps)} steps in {len(changes)} changes; {len(errors)} problems')
            return 1 if errors else 0
        elif a.cmd == 'board':
            changes, steps, errors = load(tree)
            text = render_board(tree, changes, steps)
            target = tree.path('BOARD.md')
            if a.check:
                current = read(target) if os.path.exists(target) else ''
                if current != text:
                    print('changes/BOARD.md is out of date; run `python3 scripts/changes/changes.py board`')
                    return 1
                return 0
            tree.write(target, text)
        elif a.cmd == 'status-page':
            changes, steps, errors = load(tree)
            text = status_page(tree, changes, steps)
            target = os.path.join(tree.root, STATUS_PAGE)
            if a.check:
                if read(target) != text:
                    print(f'{STATUS_PAGE} is out of date; run `python3 scripts/changes/changes.py status-page`')
                    return 1
                return 0
            tree.write(target, text)
        elif a.cmd == 'links':
            def norm(f, t):
                return re.sub(r'/(change|decisions|steps|review-history)\.md$', '/', f), t
            found = broken_links(tree, md_files(tree))
            if a.write_baseline:
                json.dump(sorted({norm(f, t) for f, t, _ in found}), open(a.write_baseline, 'w'), indent=1)
            if a.baseline:
                base_set = {tuple(x) for x in json.load(open(a.baseline))}
                new = [x for x in found if norm(x[0], x[1]) not in base_set]
                for x in new:
                    print(*x, sep=' | ')
                print(f'{len(found)} broken links; {len(new)} not in the baseline')
                return 1 if new else 0
            for x in found:
                print(*x, sep=' | ')
            print(f'{len(found)} broken links')
    except Error as e:
        print(f'error: {e}', file=sys.stderr)
        return 2
    return 0


if __name__ == '__main__':
    sys.exit(main())
