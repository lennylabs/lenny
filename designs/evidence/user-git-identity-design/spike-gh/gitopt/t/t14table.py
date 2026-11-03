import json,sys,glob,os,collections
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
for f in sorted(glob.glob('/out/t14v2/*.steps')):
    for s in map(json.loads,open(f)):
        a,b=s['start'],s['end']
        refs=[x.get('reason','')[:70] for x in na if a<=x.get('t','')<=b and x.get('decision')=='refused']
        ms=[]
        for x in og:
            if a<=x.get('t','')<=b and x.get('gql'):
                for g in x['gql']:
                    for o in g.get('ops') or []:
                        if o['type']=='mutation': ms+=o['fields']
            elif a<=x.get('t','')<=b and 'headers' in x and x.get('method') not in ('GET',None) and not x.get('gql'): ms.append(x['method']+' '+x['uri'].split('?')[0].split('/')[-1])
        print(f"{s['label']:44s} rc={s['rc']:<3} forwarded={','.join(ms)[:70]} refused={refs[0] if refs else ''}")
