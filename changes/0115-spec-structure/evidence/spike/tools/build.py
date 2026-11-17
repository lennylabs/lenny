import re, sys, collections, os, json

D = os.path.dirname(os.path.abspath(__file__))
OUT = {
    'OUT-BUILDPLAN': '`BUILD-PLAN.md` (and `BUILD-GAPS.md` for open claims)',
    'OUT-ARCHIVE': 'the archive of designs and proposals (decision records)',
    'OUT-ROADMAP': '`ROADMAP.md`',
    'OUT-ABOUT': '`docs/about/`',
    'OUT-ISSUE': 'the issue tracker',
    'OUT-OPGUIDE': '`docs/operator-guide/`',
    'OUT-RUNBOOKS': '`docs/runbooks/`',
    'OUT-RTGUIDE': '`docs/runtime-author-guide/`',
    'OUT-CLIENT': '`docs/client-guide/`',
    'OUT-REFRT': 'the reference runtime repositories and `docs/runtime-author-guide/`',
    'OUT-ADAPTER': 'the client protocol adapter repositories (`lenny-adapter-*`)',
    'OUT-REMOVED': 'removed by a design (archive only)',
    'OUT-CODE': 'Go package documentation',
    'OUT-TESTING': '`TESTING.md`',
    'OUT-CONTRIB': '`CONTRIBUTING.md` and contributor docs',
    'NONE': 'container heading with no own text',
}
pages = collections.OrderedDict()
for line in open(os.path.join(D, 'pages.tsv')):
    line = line.rstrip('\n')
    if not line: continue
    code, typ, path, summ, owner = line.split('|')
    pages[code] = dict(code=code, type=typ, path=path, summary=summ, owner=owner)

def parse_dests(s):
    s = s.strip()
    out = []
    parts = [p for p in s.split(',') if p.strip()]
    for p in parts:
        if ':' in p:
            c, w = p.split(':'); out.append((c.strip(), float(w)))
        else:
            out.append((p.strip(), None))
    n_none = sum(1 for _, w in out if w is None)
    if n_none:
        rest = 1.0 - sum(w for _, w in out if w is not None)
        out = [(c, w if w is not None else rest / n_none) for c, w in out]
    return out

# spec headings
heads = []
for line in open(os.path.join(D, 'headings.tsv')):
    f, ln, lvl, own, sub, h = line.rstrip('\n').split('\t')
    base = os.path.basename(f)
    key = 'RM' if base == 'README.md' else base[:2]
    title = re.sub(r'^#+\s+', '', h).strip()
    heads.append(dict(file=base, key='%s:%s' % (key, ln), fk=key, line=int(ln), lvl=int(lvl), own=int(own), sub=int(sub), title=title))

specmap = {}
for line in open(os.path.join(D, 'specmap.txt')):
    line = line.rstrip('\n')
    if not line.strip(): continue
    parts = [p.strip() for p in line.split('|')]
    while len(parts) < 4: parts.append('')
    key, dests, note, triple = [p.strip() for p in parts[:4]]
    specmap[key] = dict(dests=dests, note=note, triple=triple)

IF_T = {'Endpoints', 'Endpoint', 'Response', 'Response Types', 'Error Codes', 'Authorization', 'Filters', 'Status Values',
        'Event Subscription', 'Operation Kinds', 'Canonical `operationId` Format', 'Lock Struct', 'Release Channel Response',
        'Runtime API', 'Backup Progress'}
COMP_T = {'Data Sources', 'Caching', 'Implementation', 'Metrics', 'Running-State Caching', 'Clock Source',
          'Multi-Replica Behavior', 'Subscription Cache and Invalidation', 'Delivery Tracking Retention',
          'Storage Tiers', 'Storage Tiers (Create Path)', 'Storage Tiers (Query Path)', 'Reconciliation'}
CAP_T = {'Degradation', 'Behavior', 'Rules Engine', 'Health Derivation Rules', 'Alerting Rules'}

def child_rule(title, tri):
    t = dict(x.split('=') for x in tri.split(';'))
    if title in IF_T: return '%s' % t['if'], 'Interface fact under the %s/%s/%s split of this subsection family.' % (t['cap'], t['comp'], t['if'])
    if title == 'Go Interface': return 'OUT-CODE', 'Go signature; package documentation, or the component internal parts where a reader needs it.'
    if title == 'Storage': return '%s:0.5,STORPG:0.5' % t['comp'], 'Storage mechanism → component; table definitions → STORPG.'
    if title in COMP_T: return t['comp'], 'Unit fact (implementation, storage, metrics) → the component.'
    if title == 'Audit Events': return 'EVTAUD', 'Audit event schemas → EVTAUD; the emitting operation links its owner capability.'
    if title in CAP_T: return t['cap'], 'Behavioral rule or behavior under failure → the capability.'
    return t['cap'], 'Behavioral content → the capability.'

# resolve assignments
rows = []
stack = []  # (lvl, head, resolved)
for h in heads:
    while stack and (stack[-1][0] >= h['lvl'] or stack[-1][1]['file'] != h['file']):
        stack.pop()
    k = h['key']
    wild = '%s:*' % h['fk']
    how = 'explicit'
    if k in specmap:
        m = specmap[k]; dests, note = m['dests'], m['note']; triple = m['triple']
    elif wild in specmap:
        m = specmap[wild]; dests, note, triple = m['dests'], m['note'], ''
        how = 'file'
    else:
        triple = ''
        anc_tri = next((s for s in reversed(stack) if s[2].get('triple')), None)
        anc = stack[-1] if stack else None
        if anc_tri:
            dests, note = child_rule(h['title'], anc_tri[2]['triple'])
            how = 'rule'
        elif anc:
            dests, note = anc[2]['dests'], 'As its parent.'
            how = 'inherit'
        else:
            dests, note = 'UNASSIGNED', ''
            how = 'none'
    # numbered label
    mnum = re.match(r'^(\d+(?:\.\d+)*)\.?\s+(.*)$', h['title'])
    if mnum:
        label = '§%s %s' % (mnum.group(1), mnum.group(2))
    elif h['fk'] == 'RM':
        label = 'README › %s' % h['title']
    else:
        chain = []
        for s in stack:
            sh = s[1]
            mm = re.match(r'^(\d+(?:\.\d+)*)\.?\s+(.*)$', sh['title'])
            if mm: chain = ['§%s %s' % (mm.group(1), mm.group(2))]
            else: chain.append(sh['title'])
        label = ' › '.join(chain + [h['title']]) if chain else h['title']
    res = dict(dests=dests, note=note, triple=triple)
    rows.append(dict(h=h, label=label, dests=dests, note=note, how=how))
    stack.append((h['lvl'], h, res))

# words per page
spec_words = collections.Counter()
unknown = set()
for r in rows:
    if r['dests'] in ('NONE', 'UNASSIGNED'): continue
    for c, w in parse_dests(r['dests']):
        if c not in pages and c not in OUT: unknown.add(c)
        spec_words[c] += r['h']['own'] * w

# designs
dmap = collections.defaultdict(list)
dclass = {}
for line in open(os.path.join(D, 'designmap.txt')):
    line = line.rstrip('\n')
    if not line.strip(): continue
    f, pref, dests = [p.strip() for p in line.split('|')]
    if pref == '@class': dclass[f] = dests
    else: dmap[f].append((pref, dests))
design_words = collections.Counter()
drows = collections.defaultdict(list)
dtot = collections.Counter(); dnorm = collections.Counter()
for line in open(os.path.join(D, 'design_h2.tsv')):
    f, ln, lvl, own, sub, h = line.rstrip('\n').split('\t')
    base = os.path.basename(f)
    title = re.sub(r'^#+\s+', '', h).strip()
    dtot[base] += int(sub)
    match = None
    for pref, dests in dmap.get(base, []):
        t2 = title.replace('`', '')
        if t2 == pref or t2.startswith(pref + ' ') or t2.startswith(pref + ':') or (not re.match(r'^[A-Z]+-\d', pref) and t2.startswith(pref)):
            match = (pref, dests); break
    if match:
        dnorm[base] += int(sub)
        for c, w in parse_dests(match[1]):
            if c not in pages and c not in OUT: unknown.add(c)
            design_words[c] += int(sub) * w * 0.5
        drows[base].append((title, match[1], int(sub)))

json.dump(dict(rows=[dict(label=r['label'], key=r['h']['key'], file=r['h']['file'], lvl=r['h']['lvl'], own=r['h']['own'], sub=r['h']['sub'], dests=r['dests'], note=r['note'], how=r['how']) for r in rows],
               spec_words=spec_words, design_words=design_words, drows=drows, dclass=dclass, dtot=dtot, dnorm=dnorm, unknown=sorted(unknown)),
          open(os.path.join(D, 'result.json'), 'w'), indent=1)
print('rows', len(rows), 'unknown', sorted(unknown))
print(collections.Counter(r['how'] for r in rows))
print('unassigned', [r['label'] for r in rows if r['dests'] == 'UNASSIGNED'])
print('designs mapped', len(drows), 'of', len(dtot))
for f in dtot:
    if f not in dclass: print('no class', f)
