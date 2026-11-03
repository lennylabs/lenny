# t27table.py <steps>: per step: rc, identity, the credential at the origin by host, and any refusal.
import json,sys
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
for s in map(json.loads,open(sys.argv[1])):
    a,b=s['start'],s['end']
    ids=[];ref=[]
    for x in na:
        if a<=x.get('t','')<=b and x.get('gitOperation'):
            if x['decision']=='allowed': ids.append(x.get('reason',''))
            elif x['decision']=='refused': ref.append(x['reason'][:80])
    toks=set()
    for x in og:
        if a<=x.get('t','')<=b:
            t=x.get('token') or x.get('tok') or x.get('auth')
            if t is None and 'headers' in x: t=(x['headers'].get('Authorization') or ['-'])[0][:30]
            toks.add(f"{x.get('host','')}={t if t not in (None,'') else '-'}:{x.get('status','')}")
    print(f"{s['label'][:40]:40s} rc={s['rc']:<3} identity={','.join(sorted(set(ids)))[:22]:22s} origin={' '.join(sorted(toks))[:150]} refused={ref[0] if ref else ''}")
