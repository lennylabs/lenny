import os, collections
D = os.path.dirname(os.path.abspath(__file__))
ROOT = '/Users/joan/projects/lenny/designs/evidence/spec-structure-design/spike/proposed'
pages = collections.OrderedDict()
for line in open(os.path.join(D, 'pages.tsv')):
    line = line.rstrip('\n')
    if not line: continue
    code, typ, path, summ, owner = line.split('|')
    pages[code] = dict(code=code, type=typ, path=path, summary=summ, owner=owner)
bypath = {p['path']: p for p in pages.values()}

def rel(frm, to):
    return os.path.relpath(to, os.path.dirname(frm))

def title_of(p):
    t = os.path.basename(p['path'])[:-3]
    if t == 'index':
        t = os.path.basename(os.path.dirname(p['path']))
    t = t.replace('-', ' ')
    fix = {'mtls and pki': 'mTLS and PKI', 'siem export': 'SIEM export', 'llm upstreams': 'LLM upstreams', 'dns': 'DNS',
           'mcp management': 'MCP management', 'cli': 'CLI', 'runtime sdks': 'Runtime SDKs', 'kms': 'KMS',
           'postgres': 'Postgres', 'redis': 'Redis', 'kubernetes': 'Kubernetes', 'cert manager': 'cert-manager',
           'prometheus': 'Prometheus', 'netagent': 'netagent', 'lenny ops': 'lenny-ops', 'api conventions': 'API conventions',
           'admin api': 'Admin API', 'client api': 'Client API', 'ops api': 'Ops API', 'agent dns': 'Agent DNS',
           'egress decision service': 'Egress decision service', 'web playground': 'Web playground'}
    return fix.get(t, t[:1].upper() + t[1:])

def fm(code, area, summary):
    s = ['---', 'code: %s' % code, 'type: index']
    if area: s.append('area: %s' % area)
    s += ['summary: >-', '  ' + summary, '---', '']
    return '\n'.join(s)

def table(frm, ps, extra_owner=False):
    out = ['| Page | Code | Summary |', '| :-- | :-- | :-- |']
    for p in ps:
        out.append('| [%s](%s) | %s | %s |' % (title_of(p), rel(frm, p['path']), p['code'], p['summary']))
    return '\n'.join(out)

def children(folder):
    """Pages directly in folder, plus index pages of direct subfolders."""
    res = []
    for p in pages.values():
        d = os.path.dirname(p['path'])
        if d == folder and os.path.basename(p['path']) != 'index.md':
            res.append(p)
        elif os.path.dirname(d) == folder and os.path.basename(p['path']) == 'index.md':
            res.append(p)
    return res

def write(path, text):
    full = os.path.join(ROOT, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    open(full, 'w').write(text.rstrip() + '\n')

AREAS = collections.OrderedDict([
 ('runtimes-and-pools', 'Runtimes and pools'),
 ('sessions', 'Sessions'),
 ('messaging', 'Messaging'),
 ('workspaces-and-files', 'Workspaces and files'),
 ('delegation', 'Delegation'),
 ('tenancy-and-access', 'Tenancy and access'),
 ('credentials', 'Credentials'),
 ('network-and-egress', 'Network and egress'),
 ('policy-and-controls', 'Policy and controls'),
 ('billing-and-usage', 'Billing and usage'),
 ('observability-and-operations', 'Observability and operations'),
 ('security-and-compliance', 'Security and compliance'),
 ('deployment-and-lifecycle', 'Deployment and lifecycle'),
])

AREA_INFO = {
 'sessions': dict(
   intro='The capabilities in this area govern one session from its creation to its end. A session is created on a claimed pod, runs turns, may suspend and resume, is checkpointed and restored, may be derived into a new session, and ends with a seal. Sessions that share a pod are isolated in slots, and a pod is scrubbed before another session reuses it.',
   paths=[('To change what states a session takes and when, read', ['SESS', 'TIMEOUT']),
          ('To change how a session starts on a pod, read', ['SCREATE', 'CLAIM', 'MATER']),
          ('To change what survives a pod failure, read', ['RESUME', 'CKPT', 'RESTORE']),
          ('To change what one session on a shared pod can see of another, read', ['SLOT', 'SLOTOPS', 'SCRUB'])],
   comps=['GW', 'GWCOORD', 'ADAPTER', 'SUPV', 'WPC'], ifs=['CAPISES', 'CHGWPOD', 'RTPHAND', 'STOROBJ']),
 'messaging': dict(
   intro='The capabilities in this area carry messages into a session and output out of it. A client or another session sends a message, the gateway holds it in the session inbox and delivers it to the runtime one turn at a time, and the runtime\'s output, activity, and requests for input stream back to the client.',
   paths=[('To change when a message reaches the runtime, read', ['MSG', 'INBOX']),
          ('To change what a client sees while a turn runs, read', ['STREAM', 'CAPIEVT']),
          ('To change how a runtime asks for input or calls client tools, read', ['INPUT', 'CTOOL'])],
   comps=['GW', 'GWCOORD', 'ADAPTER'], ifs=['CAPIMSG', 'CAPIEVT', 'RTPMSG', 'RTPPART']),
 'workspaces-and-files': dict(
   intro='The capabilities in this area build a session\'s workspace and move files in and out of it. A workspace plan names the sources, the adapter and the materialization helper build the workspace inside the slot, and files travel by upload, with messages, by export between sessions, and as stored artifacts. Repository stores on the pod hold git repositories that sessions check out.',
   paths=[('To change how a workspace is built, read', ['WSPLAN', 'MATER', 'WSPLANIF']),
          ('To change how files reach a running session, read', ['UPLOAD', 'FILEMSG']),
          ('To change how repositories are prepared on a pod, read', ['REPO', 'CLONE', 'GIT'])],
   comps=['ADAPTER', 'SUPV', 'PODINIT', 'OBJ'], ifs=['WSPLANIF', 'CAPIFILE', 'RTPFS']),
 'delegation': dict(
   intro='The capabilities in this area let a session start child sessions. A parent delegates under a lease that its policy bounds, the children form a tree, and the tree recovers, cascades, and cleans up as its members fail or end.',
   paths=[('To change what a parent may delegate, read', ['DPOL', 'LEASE', 'DAVAIL']),
          ('To change what happens to children when a parent fails or ends, read', ['DTREE'])],
   comps=['GW'], ifs=['RTPTOOL', 'ADMPOL']),
 'tenancy-and-access': dict(
   intro='The capabilities in this area define the tenant, its settings and environments, and the principals within it, and decide who may do what. Authentication establishes the caller, token exchange issues narrower tokens, and authorization applies roles and scopes.',
   paths=[('To change how a caller is identified, read', ['AUTHN', 'PRINC']),
          ('To change who may perform an operation, read', ['AUTHZ', 'SECROLE']),
          ('To change a tenant-wide setting, read', ['TSET', 'TENANT'])],
   comps=['GW', 'TOKSVC', 'PLAY'], ifs=['ADMTEN', 'ADMIAM', 'CAPI']),
 'credentials': dict(
   intro='The capabilities in this area hold credentials and deliver them to sessions. Credential pools hold platform credentials for VCS providers, users and service accounts own credentials of their own, account links obtain them through OAuth, and request rules decide what a session receives. A user\'s VCS identity authorizes repository access and pull request authoring.',
   paths=[('To change what credentials a session receives, read', ['CREQ', 'UCRED', 'CDELIV']),
          ('To change how a user links an account, read', ['LINK']),
          ('To change pull request authoring, read', ['PRAUTH', 'REPOAUTH', 'ADDON'])],
   comps=['TOKSVC', 'NETAGENT', 'KMS'], ifs=['CAPICRED', 'ADMCRED']),
 'network-and-egress': dict(
   intro='The capabilities in this area decide what network traffic an agent pod may send and how that traffic is attributed. Network policy isolates pods by default, the egress policy decides each outbound connection, and dedicated paths serve DNS, git, LLM upstreams, operator services, and a tenant\'s private network.',
   paths=[('To change which hosts a session may reach, read', ['EGRESS', 'EGDEF', 'EGDEC']),
          ('To change how model calls are routed and metered, read', ['LLMUP', 'GWLLM']),
          ('To change how a pod reaches a private network, read', ['OUTCONN'])],
   comps=['NETAGENT', 'EGPROXY', 'EGAUTHZ', 'GITMIR', 'AGDNS', 'OUTGW', 'GWLLM'], ifs=['CHEGRESS', 'RTPFS']),
 'policy-and-controls': dict(
   intro='The capabilities in this area admit, shape, and refuse requests. The policy engine runs the admission checks and request interceptors, quotas and rate limits bound use, and operator circuit breakers stop classes of work.',
   paths=[('To change the order or content of admission checks, read', ['ADMIT', 'INTCPT']),
          ('To change a limit on use, read', ['QUOTA'])],
   comps=['GW', 'REDIS'], ifs=['INTCPTIF', 'ADMPOL']),
 'billing-and-usage': dict(
   intro='The capabilities in this area meter usage, enforce budgets, and emit billing events. Usage records come from the metering proxy and from resource attribution, rate cards price them, budgets cap them, and the billing event stream carries them to the operator.',
   paths=[('To change what a tenant is billed for, read', ['ATTR', 'USAGE', 'BILLEV']),
          ('To change budget enforcement, read', ['BUDGET', 'RATE'])],
   comps=['GW', 'GWLLM', 'WPC'], ifs=['EVTBILL', 'ADMBILL']),
 'observability-and-operations': dict(
   intro='The capabilities in this area let operators and DevOps agents observe, diagnose, and remediate the platform. Components emit operational events and health signals, lenny-ops aggregates them and runs diagnostics, and remediation locks and escalations coordinate the agents that act.',
   paths=[('To change what an agent can observe, read', ['OPSEV', 'DIAG']),
          ('To change how agents coordinate a fix, read', ['REMED', 'RUNBOOK']),
          ('To change alerting, read', ['SLO'])],
   comps=['LOPS', 'LOPSSVC', 'GWOPS', 'PROM'], ifs=['OPSAPI', 'OPSMCP', 'EVTOPS']),
 'security-and-compliance': dict(
   intro='The capabilities in this area record evidence, oversee security, and meet data obligations. The audit log records actions with integrity and exports them to a SIEM, oversight gives tenants a view of their security posture and of operator access, forensics preserves evidence from pods, and holds, erasure, and residency govern stored data.',
   paths=[('To change what is audited, read', ['AUDIT', 'SIEM']),
          ('To change what happens to data when a user is erased, read', ['ERASE', 'PSEUDO', 'HOLD']),
          ('To change incident handling, read', ['FORENSIC', 'ACTSTR'])],
   comps=['LOPS', 'PG', 'OBJ', 'GITMIR'], ifs=['EVTAUD', 'OPSAUD', 'ADMCOMP']),
 'deployment-and-lifecycle': dict(
   intro='The capabilities in this area install, size, upgrade, back up, and restore a Lenny deployment, and run it locally for development.',
   paths=[('To change what installation creates, read', ['INSTALL', 'DCFG']),
          ('To change the upgrade procedure, read', ['UPGRADE', 'COMPAT']),
          ('To change backups, read', ['BACKUP', 'BKJOB'])],
   comps=['LOPS', 'PREFLT', 'BKJOB'], ifs=['DCFG', 'DCFGANS', 'DCFGTIER', 'CLIINST']),
}

def link(frm, code):
    p = pages[code]
    return '[%s](%s)' % (title_of(p), rel(frm, p['path']))

AREA_CODE = {'sessions': 'SESAREA', 'messaging': 'MSGAREA', 'workspaces-and-files': 'WSAREA', 'delegation': 'DLGAREA',
 'tenancy-and-access': 'TENAREA', 'credentials': 'CREDAREA', 'network-and-egress': 'NETAREA', 'policy-and-controls': 'POLAREA',
 'billing-and-usage': 'BILLAREA', 'observability-and-operations': 'OPSAREA', 'security-and-compliance': 'SECAREA',
 'deployment-and-lifecycle': 'DEPAREA', 'runtimes-and-pools': 'RTPOOL'}

# area indexes
for a, name in AREAS.items():
    if a == 'runtimes-and-pools': continue
    path = 'capabilities/%s/index.md' % a
    p = bypath[path]; info = AREA_INFO[a]
    s = [fm(p['code'], a, p['summary']), '# %s\n' % name, info['intro'] + '\n', '## Reading paths\n']
    for lead, codes in info['paths']:
        s.append('- %s %s.' % (lead, ', then '.join(link(path, c) for c in codes)))
    s.append('\n## Capabilities\n')
    s.append(table(path, children('capabilities/%s' % a)))
    s.append('\n## Components that carry out this area\n')
    s.append('\n'.join('- %s: %s' % (link(path, c), pages[c]['summary']) for c in info['comps']))
    s.append('\n## Interfaces this area exposes\n')
    s.append('\n'.join('- %s: %s' % (link(path, c), pages[c]['summary']) for c in info['ifs']))
    write(path, '\n'.join(s))

# nested capability indexes
for path, code, intro in [
  ('capabilities/sessions/session-lifecycle/index.md', 'SESLC', 'The session lifecycle is split across pages by phase. The states page owns the session object and its state machine; the other pages state the rules of one phase each and link the states they move between.'),
  ('capabilities/sessions/checkpoints/index.md', 'CKIDX', 'Checkpoints are split into capture, storage, and restore. Capture decides when and how a slot\'s files are written, storage keeps and collects what was written, and restore puts it back into a slot.')]:
    p = bypath[path]
    s = [fm(code, 'sessions', p['summary']), '# %s\n' % title_of(p).capitalize(), intro + '\n', '## Pages\n', table(path, children(os.path.dirname(path)))]
    write(path, '\n'.join(s))

# capabilities/index.md
path = 'capabilities/index.md'
s = [fm('CAPS', None, pages['CAPS']['summary']), '# Capabilities\n',
     'A capability page states what Lenny does end to end: the domain objects it governs, its numbered requirements, the flows across components, its behavioral settings, its behavior under failure, and its security properties. Capabilities are grouped into areas, and each area has an index.\n',
     '## Areas\n', '| Area | Code | Summary | Capabilities |', '| :-- | :-- | :-- | :-- |']
for a, name in AREAS.items():
    ip = bypath['capabilities/%s/index.md' % a]
    caps = [c for c in children('capabilities/%s' % a)]
    s.append('| [%s](%s) | %s | %s | %s |' % (name, rel(path, ip['path']), ip['code'], ip['summary'], ', '.join(c['code'] for c in caps)))
write(path, '\n'.join(s))

# overview/index.md
path = 'overview/index.md'
ov = [p for p in pages.values() if p['path'].startswith('overview/') and p['path'] != path and not p['path'].startswith('overview/walkthroughs/')]
wk = [p for p in pages.values() if p['path'].startswith('overview/walkthroughs/')]
s = [fm('OVW', None, pages['OVW']['summary']), '# Overview\n',
     'The overview pages state Lenny\'s purpose and context and map its areas, capabilities, components, and interfaces. They state no rule, value, or behavior; each map entry links the page that does. The walkthroughs trace scenarios across components and are non-normative.\n',
     '## Pages\n', table(path, ov), '\n## Walkthroughs\n', table(path, wk)]
write(path, '\n'.join(s))

# walkthroughs index
path = 'overview/walkthroughs/index.md'
s = [fm('WALK', None, pages['WALK']['summary']), '# Walkthroughs\n',
     'Each walkthrough traces one scenario step by step. Every step names the component that acts, the interface operation or channel message it uses, and the requirement that decides the outcome, as a link. A walkthrough states no rule of its own: where a step and a requirement disagree, the requirement holds and the walkthrough is wrong.\n',
     '## Pages\n', table(path, [p for p in wk if p['path'] != path])]
write(path, '\n'.join(s))

# components/index.md
path = 'components/index.md'
own = [p for p in children('components') if not p['path'].startswith('components/external/')]
podc = [p for p in pages.values() if p['path'].startswith('components/agent-pod/')]
gwc = [p for p in pages.values() if p['path'].startswith('components/gateway/')]
opsc = [p for p in pages.values() if p['path'].startswith('components/lenny-ops/')]
ext = [p for p in pages.values() if p['path'].startswith('components/external/')]
lenny = [p for p in own if p['path'] not in ('components/agent-pod/index.md',)]
s = [fm('COMPS', None, pages['COMPS']['summary']), '# Components\n',
     'A component page describes one running unit: its responsibilities as links to the requirements it carries out, the interfaces it serves and consumes, its internal parts, its deployment, its process settings, its failure and recovery, and its metrics. A component page restates no behavioral rule.\n',
     'The gateway, lenny-ops, and the agent pod are folders. The folder index is the component page for the unit as a whole, and each sub-page covers one internal part or container.\n',
     '## Lenny processes\n', table(path, lenny),
     '\n## Gateway parts\n', table(path, [p for p in gwc if not p['path'].endswith('index.md')]),
     '\n## lenny-ops parts\n', table(path, [p for p in opsc if not p['path'].endswith('index.md')]),
     '\n## Agent pod\n', table(path, podc),
     '\n## External dependencies\n', table(path, ext)]
write(path, '\n'.join(s))

# interfaces/index.md
path = 'interfaces/index.md'
ch = children('interfaces')
s = [fm('IFS', None, pages['IFS']['summary']), '# Interfaces\n',
     'An interface page states a contract that one party can be built against without reading the other party: syntax, operations, fields with types and constraints, the errors each operation returns, authentication, idempotency, and versioning. Each outcome links the requirement that decides it. Conventions shared by several interfaces live in [API conventions](../cross-cutting/api-conventions.md) and the [error model](../cross-cutting/error-model.md).\n',
     '## Interfaces\n', '| Interface | Code | Parties | Summary |', '| :-- | :-- | :-- | :-- |']
PARTIES = {'CAPI': 'clients and the gateway', 'ADMIN': 'tenant and platform admins and the gateway', 'OPSAPI': 'operators, DevOps agents, the gateway, and lenny-ops',
 'RTP': 'the adapter and a runtime', 'ADMAN': 'the adapter and a runtime', 'INTCPTIF': 'the gateway and a request interceptor',
 'CHAN': 'the gateway, the adapter, the supervisor, and the stores', 'K8S': 'Kubernetes API clients and Lenny\'s controllers and webhooks',
 'CLI': 'operators and the APIs', 'EVT': 'Lenny and event consumers', 'WSPLANIF': 'clients and the gateway',
 'STOR': 'one Lenny version and the next', 'DCFG': 'deployers and the chart', 'EXT': 'deployers who replace a backend and Lenny\'s core'}
for p in ch:
    s.append('| [%s](%s) | %s | %s | %s |' % (title_of(p), rel(path, p['path']), p['code'], PARTIES.get(p['code'], ''), p['summary']))
write(path, '\n'.join(s))

# cross-cutting/index.md
path = 'cross-cutting/index.md'
CARRIERS = {
 'SECMODEL': 'ISO, SLOT, NETISO, EGRESS, TOKX, PLAY', 'TENISO': 'TENANT, ENV, QUOTA, AUDIT, PG, REDIS, OBJ, STORREDIS',
 'PKI': 'GW, ADAPTER, TOKSVC, LOPS, INTCPT, CERTM', 'AVAIL': 'GWCOORD, QUOTA, BREAKER, CKSTORE, PG, REDIS, OBJ, CHAN',
 'COMPAT': 'CAPI, ADMIN, RTP, K8S, STOR, UPGRADE', 'OBSCONV': 'every component Metrics section, SLO',
 'ERR': 'CAPI, ADMIN, OPSAPI, RTP', 'APICONV': 'CAPI, ADMIN, OPSAPI', 'DATACLASS': 'TSET, ARTIFACT, CKSTORE, HOLD, ERASE, OBJ',
 'NAMING': 'CHAN, RUNTIME, POOL, KLABEL'}
s = [fm('XC', None, pages['XC']['summary']), '# Cross-cutting properties\n',
     'A cross-cutting page states a property or convention that every capability and component upholds, and lists the pages that carry it. How one capability meets the property is stated on that capability\'s page.\n',
     '## Pages\n', '| Page | Code | Summary | Carried by |', '| :-- | :-- | :-- | :-- |']
for p in children('cross-cutting'):
    s.append('| [%s](%s) | %s | %s | %s |' % (title_of(p), rel(path, p['path']), p['code'], p['summary'], CARRIERS.get(p['code'], '')))
write(path, '\n'.join(s))

# README.md root map
path = 'README.md'
s = ['---', 'code: README', 'type: conventions', 'summary: >-', '  The root map of the specification: what Lenny is, the capability areas, the other top-level folders, and reading paths by task.', '---', '',
 '# Lenny specification\n',
 'Lenny is a Kubernetes-native platform that runs agent sessions for many tenants. A client creates a session through the REST API; the gateway claims a pre-warmed pod for the session\'s runtime, builds its workspace, and relays messages and output between the client and the runtime. Sessions can suspend, resume from checkpoints, and delegate work to child sessions, while the platform meters usage, enforces policy and egress rules, and exposes an operability API for human operators and DevOps agents.\n',
 'This page is the entry point. Read a folder index to find a page, read the page summary to decide whether to read on, and follow links for terms a page does not own. [Conventions](conventions.md) states the page types, the placement rules, and the identifier scheme.\n',
 '## Capability areas\n', '| Area | Summary |', '| :-- | :-- |']
for a, name in AREAS.items():
    ip = bypath['capabilities/%s/index.md' % a]
    s.append('| [%s](%s) | %s |' % (name, ip['path'], ip['summary']))
s += ['\n## Other folders\n', '| Folder | Contents |', '| :-- | :-- |',
 '| [Overview](overview/index.md) | Purpose and context, principles and non-goals, the architecture, the capability and interface maps, and non-normative walkthroughs. |',
 '| [Components](components/index.md) | One page per running unit: Lenny\'s processes, the agent pod and its containers, and the external dependencies. |',
 '| [Interfaces](interfaces/index.md) | Contracts that one party can be built against: the client, admin, and ops APIs, the runtime protocol, the channels, Lenny\'s Kubernetes API, the CLI, events, and stored and deployment schemas. |',
 '| [Cross-cutting](cross-cutting/index.md) | Properties every page upholds: the security model, tenant isolation, PKI, availability, compatibility, observability conventions, the error model, API conventions, data classification, and naming. |',
 '| [Catalogs](catalogs/index.md) | Generated lists of settings, limits, errors, metrics, alerts, events, labels, custom resources, API operations, requirements, and terms, each row pointing at its owner. |',
 '\n## Reading paths\n']
RP = [
 ('To change how a session acquires a pod', ['CLAIM', 'APOD', 'GWCLAIM']),
 ('To change how many pods a pool keeps warm', ['POOL', 'PSC']),
 ('To add a field to session creation', ['CAPISES', 'SCREATE', 'WSPLANIF']),
 ('To change what a runtime receives on its socket', ['RTP', 'RTPMSG', 'MSG']),
 ('To change which hosts a session may reach', ['EGRESS', 'EGDEC', 'NETAGENT']),
 ('To change how model calls are metered and billed', ['LLMUP', 'USAGE', 'BILLEV']),
 ('To change what survives a pod failure', ['RESUME', 'CKPT', 'RESTORE']),
 ('To change who may call an admin operation', ['AUTHZ', 'ADMIN']),
 ('To add a metric or an alert', ['OBSCONV', 'SLO']),
 ('To change what an operator agent can do', ['OPSAPI', 'REMED', 'DIAG']),
 ('To change a Helm value or its default', ['DCFG']),
 ('To find any setting, error, metric, or label by name', ['CAT-SETTINGS', 'CAT-ERRORS', 'CAT-METRICS', 'CAT-LABELS']),
]
for lead, codes in RP:
    s.append('- %s, read %s.' % (lead, ', then '.join('[%s](%s)' % (title_of(pages[c]), pages[c]['path']) for c in codes)))
write(path, '\n'.join(s))
print('ok')
