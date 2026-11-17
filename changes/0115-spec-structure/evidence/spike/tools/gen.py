import json, os, collections, re
D = os.path.dirname(os.path.abspath(__file__))
ROOT = '/Users/joan/projects/lenny/designs/evidence/spec-structure-design/spike'
exec(open(os.path.join(D, 'build.py')).read().split('pages = collections.OrderedDict()')[0])  # OUT dict
pages = collections.OrderedDict()
for line in open(os.path.join(D, 'pages.tsv')):
    line = line.rstrip('\n')
    if not line: continue
    code, typ, path, summ, owner = line.split('|')
    pages[code] = dict(code=code, type=typ, path=path, summary=summ, owner=owner)
R = json.load(open(os.path.join(D, 'result.json')))
sw, dw = R['spec_words'], R['design_words']
def est(c): return sw.get(c, 0) + dw.get(c, 0)

def isindex(p): return p['path'].endswith('index.md') or p['code'] in ('README',)

OUTNAME = {k: v for k, v in OUT.items()}
SHORT = {'OUT-REMOVED': 'removed', 'OUT-ARCHIVE': 'archive', 'OUT-BUILDPLAN': 'BUILD-PLAN.md', 'OUT-ROADMAP': 'ROADMAP.md',
 'OUT-ABOUT': 'docs/about', 'OUT-ISSUE': 'issues', 'OUT-OPGUIDE': 'operator guide', 'OUT-RUNBOOKS': 'runbooks',
 'OUT-RTGUIDE': 'runtime author guide', 'OUT-CLIENT': 'client guide', 'OUT-REFRT': 'reference runtimes',
 'OUT-ADAPTER': 'adapter repos', 'OUT-CODE': 'code docs', 'OUT-TESTING': 'TESTING.md', 'OUT-CONTRIB': 'contributor docs', 'NONE': 'none'}

def fmt_dests(d):
    if d in ('NONE',): return 'None (container)'
    parts = []
    for p in d.split(','):
        p = p.strip()
        if ':' in p:
            c, w = p.split(':'); w = float(w)
        else:
            c, w = p, None
        name = SHORT.get(c, c)
        parts.append('%s %d%%' % (name, round(w * 100)) if w is not None and len(d.split(',')) > 1 else name)
    return ', '.join(parts)

# ---------- size flags ----------
SPLITS = {
 'POOL': 'Slice-owned. Split into pools-and-pod-sets (definitions, pod sets, disruption) and warm-inventory (targets, SDK-warm, demotion breaker, bootstrap).',
 'CLONE': 'Split into clone declaration and refresh (what is cloned, when, on which pods) and clone credentials and failure.',
 'EGRESS': 'Split into egress-policy (model and effective policy) and egress-scopes (configuration by scope and tenant pool overlays).',
 'WPC': 'Slice-owned. Move etcd and API-server tuning to the Kubernetes dependency page (already 10% of §4.6.1 goes there).',
 'CKPT': 'Split quiesce, timeout, and abort handling into its own page beside capture passes.',
 'GW': 'Move the metric definitions of each subsystem to its sub-page (coordination, drain, metering proxy, ops endpoints).',
 'APOD': 'Slice-owned. Move the runtime-exit outcomes table to the runtime supervisor component.',
 'CDELIV': 'Split delivery modes from the file-delivery mechanics and per-slot credential files.',
}

def sizes_table(codes):
    rows = []
    for c in codes:
        p = pages[c]
        if isindex(p):
            continue
        e = est(c)
        flag = 'over' if e > 6000 else ('near' if e > 5000 else '')
        rows.append((c, p, e, flag))
    return rows

GROUPS = [
 ('Overview', lambda p: p['path'].startswith('overview/')),
 ('Capabilities', lambda p: p['path'].startswith('capabilities/')),
 ('Components', lambda p: p['path'].startswith('components/')),
 ('Interfaces', lambda p: p['path'].startswith('interfaces/')),
 ('Cross-cutting', lambda p: p['path'].startswith('cross-cutting/')),
 ('Catalogs', lambda p: p['path'].startswith('catalogs/')),
 ('Conventions', lambda p: p['path'] in ('README.md', 'conventions.md')),
]
AREA_ORDER = ['runtimes-and-pools', 'sessions', 'messaging', 'workspaces-and-files', 'delegation', 'tenancy-and-access',
              'credentials', 'network-and-egress', 'policy-and-controls', 'billing-and-usage',
              'observability-and-operations', 'security-and-compliance', 'deployment-and-lifecycle']
AREA_NAME = {a: a.replace('-and-', ' and ').replace('-', ' ').capitalize() for a in AREA_ORDER}

def sortkey(p):
    path = p['path']
    parts = path.split('/')
    # index first within its folder
    return [ (0 if x == 'index.md' else 1, x) for x in parts ]

def owner_label(o):
    return {'mine': 'map', 'slice': 'slice', 'proposed': 'proposed to slice'}[o]

def page_row(p):
    e = est(p['code'])
    ew = '' if isindex(p) or p['type'] == 'catalog' or p['type'] == 'conventions' else ('new' if e < 50 else '{:,}'.format(int(round(e, -2))))
    t = 'index' if isindex(p) and p['code'] != 'README' else p['type']
    return '| `%s` | %s | %s | %s | %s | %s |' % (p['path'], t, p['code'], p['summary'], ew, owner_label(p['owner']))

HDR = '| Path | Type | Code | Summary | Est. words | Written by |\n| :-- | :-- | :-- | :-- | --: | :-- |'

m = []
m.append('# Proposed specification tree: map\n')
m.append('Status: spike output, 2026-11-16. This file lists every page of the proposed tree under `proposed/`, with its type, its code, a one-line summary, and an estimate of its size. Paths are relative to `proposed/`. The "Written by" column records which half of the spike owns the page: "slice" pages are written in full by the vertical slice, "map" pages are named and summarized here only, and "proposed to slice" pages are catalogs this map adds to the slice\'s catalog list.\n')
m.append('The size estimate is the words of the current specification sections assigned to the page, weighted by the share given in `ASSIGNMENT.md`, plus half the words of the design sections assigned to it. Designs carry evidence tags, current-state notes, and rationale that a page does not keep, so half is the working ratio. Index, catalog, and conventions pages have no estimate. `ASSIGNMENT.md` lists the pages estimated over 6,000 words and the split each one takes.\n')
counts = collections.Counter()
for p in pages.values():
    t = 'index' if isindex(p) and p['code'] != 'README' else p['type']
    counts[t] += 1
m.append('## Page counts\n')
m.append('| Type | Pages |\n| :-- | --: |')
for t in ['overview', 'capability', 'component', 'interface', 'cross-cutting', 'catalog', 'conventions', 'index']:
    m.append('| %s | %d |' % (t, counts[t]))
m.append('| Total | %d |\n' % sum(counts.values()))
m.append('Index pages are counted once under "index" and not under the type of their folder. The overview count includes the walkthrough pages.\n')

for gname, pred in GROUPS:
    m.append('## %s\n' % gname)
    if gname == 'Capabilities':
        m.append('Each capability area is a folder under `capabilities/`. A capability larger than one page becomes a folder with its own index (`sessions/session-lifecycle/`, `sessions/checkpoints/`).\n')
        top = [p for p in pages.values() if p['path'] == 'capabilities/index.md']
        m.append(HDR); m.extend(page_row(p) for p in top); m.append('')
        for a in AREA_ORDER:
            m.append('### %s\n' % AREA_NAME[a])
            ps = sorted([p for p in pages.values() if p['path'].startswith('capabilities/%s/' % a)], key=sortkey)
            m.append(HDR); m.extend(page_row(p) for p in ps); m.append('')
        continue
    if gname == 'Interfaces':
        ps = sorted([p for p in pages.values() if pred(p)], key=sortkey)
        m.append('Each interface is either one page or a folder whose index states the conventions of that interface.\n')
        m.append(HDR); m.extend(page_row(p) for p in ps); m.append('')
        continue
    ps = sorted([p for p in pages.values() if pred(p)], key=sortkey)
    if gname == 'Components':
        m.append('Lenny\'s own processes come first, then the agent pod and its containers, then the external dependencies under `components/external/`. The gateway, lenny-ops, and the agent pod are folders with sub-pages; `FINDINGS-map.md` gives the reasons.\n')
    if gname == 'Catalogs':
        m.append('The slice owns `catalogs/`. The paths below follow the catalog list of `TAXONOMY.md`; the slice\'s files decide the final names. The last two rows are catalogs this map proposes.\n')
    if gname == 'Overview':
        m.append('The walkthroughs are non-normative. Each traces one scenario and links the requirements and operations it passes through; none states a rule.\n')
    m.append(HDR); m.extend(page_row(p) for p in ps); m.append('')

# leaves
m.append('## What leaves the specification\n')
m.append('Each row names a destination outside the specification, what goes there, the current sections it comes from, and the words of current specification text it takes. Design words are not counted here.\n')
LEAVE = [
 ('OUT-BUILDPLAN', 'The phased build sequence, phase exit criteria, the hard prerequisite chain, roll-forward notes, and the rows of the channel claim register that record absent behavior.', '§18 Build Sequence; §27.10 Roll-forward notes; part of §28.4 Claim register'),
 ('OUT-ARCHIVE', 'The resolved-decisions table, which the designs and proposals already record as decision records, and the design sections that record history, alternatives, measurements, and validation.', '§19 Resolved Decisions; the history, alternatives, benchmarks, and review sections of every design'),
 ('OUT-ROADMAP', 'Planned post-v1 work.', '§21 Planned / Post-V1'),
 ('OUT-ISSUE', 'Open questions.', '§20 Open Questions'),
 ('OUT-ABOUT', 'The competitive landscape, comparison matrices, adoption strategy, and the "why Lenny" text.', '§23 Competitive Landscape'),
 ('OUT-OPGUIDE', 'Sizing guidance, worked examples, the tier promotion guide, operator decision checklists, restore workflow, and the external memory pattern.', '§17.8.2 (part), §17.8.3, §12.3 and §12.4 (sizing parts), §25.4 Multi-Replica Lenny-Ops Scaling (part), §25.11 Restore Workflow (part), §9.4 (part), §5.1 Worked Examples (part)'),
 ('OUT-RUNBOOKS', 'Runbook bodies and step-by-step procedures. The runbooks already exist under `docs/runbooks/`; the specification keeps only the runbook format and discovery.', '§17.7 Operational Runbooks; §25.15 Total-Outage Recovery Runbook; §4.9.1 KMS Key Rotation Procedure (part); the example runbook inside §25.7'),
 ('OUT-RTGUIDE', 'The sample echo runtime, the runtime author roadmap, integration guidance by level, and plugging in a custom runtime.', '§15.4.4, §15.4.5, §15.4.3 (part), §17.4 Plugging in a custom runtime'),
 ('OUT-CLIENT', 'The client SDK list.', '§15.6 Client SDKs'),
 ('OUT-REFRT', 'Each reference runtime\'s description, its image, and its configuration. Runtime categories leave the specification.', '§26 Reference Runtime Catalog; §5.1 First-Party Reference Runtimes'),
 ('OUT-ADAPTER', 'Every client protocol other than REST: the MCP client API, OpenAI Completions and Responses, Anthropic Messages, A2A, the translation tables, and protocol version negotiation.', '§15 introduction, §15.2 MCP API, §15.2.1, the protocol columns of the Translation Fidelity Matrix, §9.1 (part), §9.2 (MCP forms)'),
 ('OUT-REMOVED', 'Mechanisms a design removes: experiments and evaluations, connectors, the memory store, the semantic cache, LLM credential leasing and direct mode, the adapter framework, `type: mcp` runtimes, service mode, the embedded adapter model, ADR labels, and the agent-sandbox dependency text.', '§10.7; §9.3; §9.4; §4.9 (most); §15 Shared Adapter Types; parts of §5.1, §5.2, §4.7.10, §13.3, and many others'),
 ('OUT-CODE', 'Go signatures and package lists that are implementation rather than contract.', '§4.0 package list; the Go Interface subsections of §25; §12.6 (part)'),
 ('OUT-TESTING', 'Benchmark and calibration methodologies, smoke tests, CI contracts, and test-requirement statements.', '§4.1 calibration methodology; §4.1 Gateway Internal Subsystems calibration; §6.3 (part); §15.4.6 (part); §17.4 Smoke test'),
 ('OUT-CONTRIB', 'Contributor procedures: source mode, runbook authoring guidelines, and adding a reference runtime.', '§17.4 Source Mode (part); §25.7 Runbook Authoring Guidelines (part); §26.12'),
]
m.append('| Destination | What goes there | Current sections | Spec words |\n| :-- | :-- | :-- | --: |')
for code, what, src in LEAVE:
    m.append('| %s | %s | %s | {:,} |'.format(int(round(sw.get(code, 0), -1))) % (OUTNAME[code], what, src))
tot_out = sum(v for k, v in sw.items() if k.startswith('OUT'))
tot_all = sum(sw.values())
m.append('')
m.append('Of %s words of current specification text with an assignment, about %s (%d%%) leave the specification under the rows above, and about %s of those are removals by designs.\n' % (
    '{:,}'.format(int(tot_all)), '{:,}'.format(int(round(tot_out, -2))), round(100 * tot_out / tot_all), '{:,}'.format(int(round(sw.get('OUT-REMOVED', 0), -2)))))
open(os.path.join(ROOT, 'proposed/MAP.md'), 'w').write('\n'.join(m) + '\n')

# ---------------- ASSIGNMENT ----------------
a = []
a.append('# Assignment of current material to the proposed tree\n')
a.append('Status: spike output, 2026-11-16. This file assigns every heading of the current specification, every design, and every page under `docs/reference/` to its destination in the proposed tree. Page codes resolve through `MAP.md`. A destination that leaves the specification names its target in words. Percentages give the share of a section\'s own words that each destination receives; they are estimates made from the section\'s bold paragraph labels, and they feed the size estimates at the end of this file.\n')
a.append('Short destination names: ' + '; '.join('"%s" is %s' % (SHORT[k], OUTNAME[k]) for k in SHORT if k != 'NONE') + '.\n')
a.append('Sections are cited by number and heading. An unnumbered heading is cited by the path from its nearest numbered ancestor. "Own words" counts the words under a heading up to its first subheading, outside code fences.\n')

# coverage
rows = R['rows']
how = collections.Counter(r['how'] for r in rows)
lv = collections.Counter(r['lvl'] for r in rows)
a.append('## Coverage\n')
a.append('- The current specification has %d headings outside code fences: %d at level 1, %d at level 2, %d at level 3, and %d at level 4. `grep -n "^#" spec/*.md` returns 760 lines; the other 64 are comment lines inside code fences. No heading is deeper than level 4.' % (len(rows), lv[1], lv[2], lv[3], lv[4]))
a.append('- Every heading is assigned: %d by an explicit row, %d by the subsection rule below, and %d by a whole-file rule (§18 and §23 leave the specification whole). None is unassigned.' % (how['explicit'], how['rule'], how['file']))
none_n = sum(1 for r in rows if r['dests'] == 'NONE')
a.append('- %d headings are containers with no own text (for example "## 4. System Components" and "### 4.6 Pod Lifecycle Controllers"); their content is assigned through their subsections.' % none_n)
a.append('- **Subsection rule.** Most subsections of §25 follow one template (Endpoints, Go Interface, Data Sources, Storage, Degradation, Metrics, Error Codes, Audit Events). For a subsection family whose parent row names a capability, a component, and an interface, a child titled Endpoints, Response, Error Codes, Authorization, Filters, or Status Values goes to the interface; Go Interface leaves to package documentation; Data Sources, Caching, Implementation, Metrics, and storage tiers go to the component; Storage splits between the component and the Postgres stored schema; Degradation, Behavior, Rules Engine, and Alerting Rules go to the capability; Audit Events go to the audit record interface.\n')

# per file tables
files = []
for r in rows:
    if not files or files[-1][0] != r['file']: files.append((r['file'], []))
    files[-1][1].append(r)
a.append('## Specification headings\n')
for f, rs in files:
    a.append('### `spec/%s`\n' % f)
    a.append('| Current heading | Own words | Destination | Split note |\n| :-- | --: | :-- | :-- |')
    for r in rs:
        note = r['note']
        if r['how'] == 'rule': note = '(subsection rule) ' + note
        if r['how'] == 'file' and not note: note = 'Whole section leaves.'
        lab = r['label'].replace('|', '\\|')
        a.append('| %s%s | %d | %s | %s |' % ('&nbsp;&nbsp;' * max(0, r['lvl'] - 2), lab, r['own'], fmt_dests(r['dests']), note.replace('|', '\\|')))
    a.append('')

# designs
a.append('## Designs\n')
a.append('Each design is treated as implemented. "Lands" means the section becomes current behavior on the named pages; its words count toward the size estimates at half weight. Sections not listed (Problem, Scope, Terms, High-level design, Decisions, Current state, Alternatives rejected, Validation owed, Tests, Specification changes, Non-specification changes, Documentation, Instructions for reviewers, Proposal split, Sources, and Review history) go to the archive of designs. A design\'s Terms table supplies glossary entries to the pages that own each term.\n')
a.append('| Design | Words | Class | Section → destination |\n| :-- | --: | :-- | :-- |')
for f in sorted(R['dtot']):
    drs = R['drows'].get(f, [])
    cells = []
    for title, dests, w in drs:
        t = re.sub(r'^([A-Z]+-\d+|DR-1 to DR-3)\b.*$', r'\1', title) if re.match(r'^[A-Z]+-\d', title) else title
        cells.append('%s → %s' % (t.replace('|', '\\|'), fmt_dests(dests)))
    a.append('| `%s` | {:,} | %s | %s |'.format(R['dtot'][f]) % (f, R['dclass'].get(f, ''), '; '.join(cells)))
a.append('')

# docs/reference
a.append('## Documentation reference pages\n')
a.append('The pages under `docs/reference/` restate specification content today. In the proposed tree each one is generated from a catalog, or reduced to a guide that links the owning pages.\n')
a.append('| Page | Words | Proposed treatment |\n| :-- | --: | :-- |')
DOCS = [
 ('adapter-contract.md', 9462, 'Reduce to a runtime-author guide page that links `interfaces/runtime-protocol/` (RTP and its sub-pages) and `interfaces/adapter-manifest.md` (ADMAN). Its frame tables are generated from the definition blocks of RTPMSG, RTPOPS, and RTPTOOL. The gateway-to-adapter gRPC section leaves the page, because no runtime author builds against it; CHGWPOD owns it.'),
 ('cloudevents-catalog.md', 858, 'Generate from the events catalog (`catalogs/events.md`), whose rows come from EVTOPS, EVTBILL, EVTAUD, and EVTHOOK.'),
 ('configuration.md', 6646, 'Generate from the settings catalog (`catalogs/settings.md`). Each Helm value is a setting block on the capability (behavioral settings) or the component (process settings) that owns it.'),
 ('error-catalog.md', 6182, 'Generate from the errors catalog (`catalogs/errors.md`), whose rows come from each interface page and from ERR for shared codes.'),
 ('execution-modes.md', 1838, 'Delete. Service mode is removed (agent-runtimes-only AR-2). The sessionPolicy matrix and presets are setting blocks on SCRUB, SLOT, and APOD, and the decision guide moves to `docs/operator-guide/`.'),
 ('glossary.md', 3915, 'Generate from the glossary catalog (`catalogs/glossary.md`), whose rows come from the Terms section of every page.'),
 ('index.md', 457, 'Replace with a pointer to `catalogs/index.md` and the generated pages.'),
 ('metrics.md', 8223, 'Generate from the metrics catalog (`catalogs/metrics.md`), whose rows come from the Metrics section of each component.'),
 ('state-machines.md', 4182, 'Generate from the proposed state-machine catalog (CAT-STATES), whose rows come from the model blocks of SESS, APOD, DLG, REV, and UPGRADE. The pool upgrade state machine it draws is replaced by the runtime rollout (runtime-revisions RV-4).'),
 ('workspace-plan.md', 1002, 'Generate from the field blocks of WSPLANIF, or link it.'),
]
for p, w, t in DOCS:
    a.append('| `docs/reference/%s` | {:,} | %s |'.format(w) % (p, t))
a.append('')

# non-spec
a.append('## Sections that leave the specification\n')
a.append('| Current section | Words | Destination | Note |\n| :-- | --: | :-- | :-- |')
NONSPEC = [
 ('§18 Build Sequence', '18_build-sequence.md', '`BUILD-PLAN.md`', 'A plan rather than a contract. Phase exit criteria that state a requirement move to the requirement\'s page; the rest is the plan.'),
 ('§19 Resolved Decisions', '19_resolved-decisions.md', 'The archive of designs and proposals', 'The decision-records design makes designs and proposals the decision records. Each row\'s current rationale moves to the Rationale section of its owner page.'),
 ('§20 Open Questions', '20_open-questions.md', 'The issue tracker', 'The section holds one line.'),
 ('§21 Planned / Post-V1', '21_planned-post-v1.md', '`ROADMAP.md`', 'Its A2A and Agent Protocol items also leave Lenny under rest-only-client-api RO-11.'),
 ('§22 Explicit Non-Decisions', '22_explicit-non-decisions.md', 'PRIN (stays)', 'The items are non-goals with reasons, so they stay in the overview rather than leaving.'),
 ('§23 Competitive Landscape', '23_competitive-landscape.md', '`docs/about/`', 'Comparison with other products.'),
 ('§26 Reference Runtime Catalog', '26_reference-runtime-catalog.md', 'Reference runtime repositories and `docs/runtime-author-guide/`', 'Runtime categories leave the specification. Conformance obligations stay in RTPCONF.'),
 ('§29 Communication Scenarios', '29_communication-scenarios.md', '`overview/walkthroughs/` (stays, non-normative)', 'Each scenario becomes one walkthrough page that links requirements and operations and states no rule.'),
 ('§17.7 Operational Runbooks', '', '`docs/runbooks/`', 'The runbook bodies; the machine-consumable format stays in RUNBOOK.'),
 ('§17.8.3 Tier Promotion Guide', '', '`docs/operator-guide/`', 'A procedure.'),
 ('README › Table of Contents', 'README.md', '`README.md` root map and index pages', 'A hand-kept table of contents.'),
]
filewords = collections.Counter()
for r in rows:
    if r['lvl'] <= 2: pass
    filewords[r['file']] += r['own']
for sec, f, dest, note in NONSPEC:
    w = filewords.get(f, 0) if f else next((r['sub'] for r in rows if r['label'].startswith(sec)), 0)
    a.append('| %s | {:,} | %s | %s |'.format(w) % (sec, dest, note))
a.append('')

# sizes
a.append('## Page size estimates\n')
a.append('The estimate is the weighted words of the specification sections assigned to the page plus half the words of the design sections assigned to it. A fully written page adds front matter, a summary, a terms table, and definition blocks, and drops restatements; the slice\'s pages measure that difference. Pages written by the slice are listed for completeness, and their splits are proposals to the slice.\n')
allp = [c for c, p in pages.items() if not isindex(p) and p['type'] not in ('catalog', 'conventions')]
ests = sorted(((est(c), c) for c in allp), reverse=True)
over = [(e, c) for e, c in ests if e > 6000]
near = [(e, c) for e, c in ests if 5000 < e <= 6000]
a.append('### Pages estimated over 6,000 words\n')
a.append('| Page | Code | Spec words | Design words (half) | Estimate | Proposed split |\n| :-- | :-- | --: | --: | --: | :-- |')
for e, c in over:
    a.append('| `%s` | %s | {:,} | {:,} | {:,} | %s |'.format(int(sw.get(c, 0)), int(dw.get(c, 0)), int(e)) % (pages[c]['path'], c, SPLITS.get(c, '')))
a.append('')
a.append('The map already splits these subjects, which a first pass estimated over budget: the gateway (GWSCALE), lenny-ops (LOPSK8S, LOPSSVC), checkpoint capture (CKPASS), egress defaults (EGDEF), message parts (RTPPART), slot assignment and failure (SLOTOPS), credential request rules (CREQ), security roles (SECROLE), lease extension (LEASEXT), pod volumes (PODVOL), owned pools (OWNPOOL), browser sessions (BROWSER), pseudonymization and receipts (PSEUDO), and the scrub procedure (SCRUBPROC). Before those splits, lenny-ops was estimated at 13,800 words, checkpoint capture at 10,600, the runtime message frames at 9,100, and the gateway at 9,400.\n')
a.append('### Pages estimated between 5,000 and 6,000 words\n')
a.append('| Page | Code | Estimate |\n| :-- | :-- | --: |')
for e, c in near:
    a.append('| `%s` | %s | {:,} |'.format(int(e)) % (pages[c]['path'], c))
a.append('')
a.append('### Distribution\n')
bins = [(0, 500), (500, 1000), (1000, 2000), (2000, 3000), (3000, 4000), (4000, 5000), (5000, 6000), (6000, 99999)]
a.append('| Estimated words | Pages |\n| :-- | --: |')
for lo, hi in bins:
    n = sum(1 for e, c in ests if lo <= e < hi)
    a.append('| %s | %d |' % ('%s to %s' % ('{:,}'.format(lo), '{:,}'.format(hi)) if hi < 99999 else 'over 6,000', n))
a.append('')
med = sorted(e for e, c in ests)[len(ests) // 2]
kept = sum(v for k, v in sw.items() if not k.startswith('OUT') and k in pages)
a.append('%d content pages have an estimate. The median is about {:,} words. The pages hold about {:,} words of current specification text and about {:,} words from designs at half weight; the current specification has {:,} words, of which the rest leaves under the rows of `MAP.md`.\n'.format(int(round(med, -2)), int(round(kept, -3)), int(round(sum(dw.get(c, 0) for c in allp), -3)), int(round(sum(r['own'] for r in rows), -3))) % len(ests))
a.append('### All estimates\n')
a.append('| Code | Path | Spec words | Design words (half) | Estimate |\n| :-- | :-- | --: | --: | --: |')
for e, c in ests:
    a.append('| %s | `%s` | {:,} | {:,} | {:,} |'.format(int(sw.get(c, 0)), int(dw.get(c, 0)), int(e)) % (c, pages[c]['path']))
open(os.path.join(ROOT, 'proposed/ASSIGNMENT.md'), 'w').write('\n'.join(a) + '\n')

json.dump({c: est(c) for c in pages}, open(os.path.join(D, 'est.json'), 'w'))
print('ok', len(over), len(near), len(ests), int(med))
print([ (c, int(e)) for e, c in ests if e < 300])
