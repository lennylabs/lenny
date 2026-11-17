# t21table.py <steps>: per step: the credential at the origin, the identity netagent logged, and any refusal.
import json,sys
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
for s in map(json.loads,open(sys.argv[1])):
    a,b=s['start'],s['end']
    ids=[];ref=[];chal=0
    for x in na:
        if a<=x.get('t','')<=b and x.get('gitOperation'):
            if x['decision']=='allowed': ids.append(x.get('reason',''))
            elif x['decision']=='challenged': chal+=1
            elif x['decision']=='refused': ref.append(x['reason'][:70])
    toks=[]
    for x in og:
        if a<=x.get('t','')<=b:
            t=x.get('token') or x.get('tok')
            if t is None and 'headers' in x:
                t=(x['headers'].get('Authorization') or [''])[0][:30]
            k=x.get('kind') or x.get('uri','').split('?')[0].split('/')[-1]
            toks.append(f"{t}:{x.get('status','')}:{k}"[:48])
    print(f"{s['label'][:44]:44s} rc={s['rc']:<3} identity={','.join(sorted(set(ids)))[:40]:40s} challenged={chal} origin={' '.join(sorted(set(toks)))[:150]} refused={ref[0] if ref else ''}")
