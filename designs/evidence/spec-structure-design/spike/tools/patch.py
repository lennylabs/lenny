import re
rep = {
'04:40': 'GW:0.25,GWSCALE:0.3,DCFGTIER:0.1,OUT-TESTING:0.25,ARCH:0.1',
'04:99': 'GW:0.3,GWSCALE:0.3,OUT-TESTING:0.3,OUT-REMOVED:0.1',
'10:76': 'GWSCALE:0.7,DCFGTIER:0.3',
'17:965': 'DCFGTIER:0.35,OUT-OPGUIDE:0.35,POOL:0.1,GWSCALE:0.1,PG:0.05,REDIS:0.05',
'16:3': 'GW:0.2,GWSCALE:0.1,GWCOORD:0.05,GWLLM:0.05,GWCLAIM:0.05,WPC:0.15,PSC:0.05,TOKSVC:0.03,ADAPTER:0.07,LOPSSVC:0.05,EGPROXY:0.02,EGAUTHZ:0.03,NETAGENT:0.03,PG:0.02,REDIS:0.02,OBJ:0.02,OUT-REMOVED:0.06',
'25:810': 'LOPSK8S', '25:1028': 'LOPSK8S', '25:1067': 'LOPSK8S', '25:1074': 'LOPSK8S', '25:1127': 'LOPSK8S', '25:1325': 'LOPSK8S',
'25:2254': 'REMED:0.5,LOPSSVC:0.5', '25:2447': 'LOPSSVC', '25:2060': 'LOPSSVC', '25:2092': 'LOPSSVC',
'25:3745': 'OPSAUD:0.5,LOPSSVC:0.5', '25:2031': 'APICONV:0.5,LOPSSVC:0.5',
'28:523': 'RTPMSG:0.33,RTPPART:0.17,RTPOPS:0.15,RTPTOOL:0.12,CHINTRA:0.13,OUT-REMOVED:0.1',
'15:1560': 'RTPPART', '15:1586': 'OUT-ADAPTER:0.8,RTPPART:0.2', '15:1641': 'RTPPART:0.6,INBOX:0.2,IMSG:0.2',
'05:395': 'SCRUB:0.15,SCRUBPROC:0.2,SLOT:0.1,SLOTOPS:0.15,APOD:0.1,POOL:0.08,CLAIM:0.04,SUPV:0.05,TIMEOUT:0.02,OUT-REMOVED:0.11',
'04:659': 'CHGWPOD:0.4,ADAPTER:0.15,SEND:0.1,SLOTOPS:0.15,CLAIM:0.1,SCRUB:0.1',
'08:625': 'LEASEXT', '08:652': 'LEASEXT', '08:712': 'LEASEXT',
'12:780': 'ERASE:0.28,PSEUDO:0.12,RESID:0.18,HOLD:0.15,TENANT:0.12,BACKUP:0.05,DATACLASS:0.05,ARTIFACT:0.02,OUT-REMOVED:0.03',
'12:28': 'PG:0.6,TENISO:0.1,OUT-OPGUIDE:0.25,AVAIL:0.05',
'27:72': 'BROWSER:0.4,PLAY:0.2,OUT-REMOVED:0.4', '27:55': 'BROWSER:0.6,PLAY:0.4',
'10:205': 'AUTHN:0.65,BROWSER:0.1,TOKSVC:0.1,PLAY:0.15',
}
notes = {
'05:395': 'Pool dimensions, onPoolExhausted, pinned idle inventory → POOL; tenant pinning → CLAIM; maxClientIdleSeconds → TIMEOUT; T4 reuse prohibition, scrub model, deployer acknowledgements, client visibility of weak reuse → SCRUB; Lenny scrub procedure, supervised scrub, what the scrub reaches, Sweep, Kata variant, onScrubFailure → SCRUBPROC; slot and reclaim hold → SLOT; concurrent-session slot failure, assignment atomicity, rehydration, slot retry → SLOTOPS; recycle lifecycle, keep pod, retirement policy → APOD; runtime supervisor → SUPV; service mode and stateless runtimes removed (agent-runtimes-only AR-2).',
'12:780': 'T4 ephemeral workspace isolation → DATACLASS; legal hold, checkpoint rotation, hold reconciler → HOLD; erasure, user-level invocation, erasure job, lifecycle fence, processing restriction, backends in scope, DeleteByUser order → ERASE; erasure_salt, pseudonymization, billing erasure, receipts → PSEUDO; tenant deletion, hold interaction, SLA → TENANT; backups in erasure scope and post-restore reconciler → ERASE and BACKUP; workspace deduplication → ARTIFACT; residency, admission control, KMS and backup residency, multi-region → RESID; MemoryStore preflight removed.',
'10:205': 'Claims, tenant_id extraction, verifier key set and rotation → AUTHN; signing topology and KMS failure → TOKSVC; playground auth paths and mint invariants → BROWSER, cookie handling in the process → PLAY.',
'27:72': 'Cookie session and token mint rules → BROWSER; process-side handling → PLAY; the MCP bearer and WebSocket wording leave (RO-12).',
}
out = []
for line in open('specmap.txt'):
    parts = [p.strip() for p in line.rstrip('\n').split('|')]
    k = parts[0]
    if k in rep:
        parts[1] = rep[k]
        if k in notes: parts[2] = notes[k]
        line = ' | '.join(parts) + '\n'
    if len(parts) > 3 and 'comp=LOPS;' in parts[3]:
        parts[3] = parts[3].replace('comp=LOPS;', 'comp=LOPSSVC;')
        line = ' | '.join(parts) + '\n'
    out.append(line)
open('specmap.txt', 'w').writelines(out)
drep = {
('incremental-checkpoint-design.md','IC-1'): 'CKPASS:0.8,SUPV:0.2',
('incremental-checkpoint-design.md','IC-7'): 'CKPASS:0.6,GWDRAIN:0.4',
('incremental-checkpoint-design.md','IC-8'): 'CKPT:0.5,CKPASS:0.5',
('egress-and-git-design.md','Defaults, replacement, and augmentation'): 'EGDEF',
('egress-and-git-design.md','TLS interception'): 'EGDEF',
('session-isolation-design.md','SI-3'): 'SUPV:0.4,SLOTOPS:0.3,SETUP:0.3',
('session-isolation-design.md','SI-4'): 'SLOTOPS:0.6,KAGENTPOD:0.4',
('user-credential-delivery-design.md','UC-4'): 'CREQ',
('security-oversight-design.md','SO-1'): 'SECROLE',
('security-oversight-design.md','SO-5'): 'SECROLE:0.6,SECOV:0.4',
('security-oversight-design.md','SO-7'): 'LOPS:0.5,SECROLE:0.5',
('agent-pod-layout-design.md','PL-5'): 'PODVOL',
('agent-pod-layout-design.md','PL-10'): 'PODVOL:0.6,QUOTA:0.4',
('tenant-derived-runtimes-design.md','DR-5'): 'OWNPOOL:0.6,POOL:0.2,ADMPOOLS:0.2',
('tenant-derived-runtimes-design.md','DR-10'): 'OWNPOOL:0.6,PLACE:0.2,ATTR:0.2',
('github-credential-addon-design.md','GH-2'): 'ADDON:0.6,PRAUTH:0.4',
('gitlab-credential-addon-design.md','GL-2'): 'ADDON:0.6,PRAUTH:0.4',
}
out = []
for line in open('designmap.txt'):
    p = [x.strip() for x in line.rstrip('\n').split('|')]
    if len(p) == 3 and (p[0], p[1]) in drep:
        p[2] = drep[(p[0], p[1])]; line = ' | '.join(p) + '\n'
    out.append(line)
open('designmap.txt', 'w').writelines(out)
