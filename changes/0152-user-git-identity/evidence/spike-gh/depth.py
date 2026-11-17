import json,glob
mx={}
for f in glob.glob('/o/raw/capture-*/*.jsonl'):
    for l in open(f):
        for g in json.loads(l).get('gql',[]):
            d=m=0; s=False
            for c in g['doc']:
                if c=='"': s=not s
                if s: continue
                if c=='{': d+=1; m=max(m,d)
                if c=='}': d-=1
            k=(g.get('ops') or [{}])[0].get('name','')
            if m>mx.get(k,(0,))[0]: mx[k]=(m,len(g['doc']))
for k,v in sorted(mx.items(),key=lambda x:-x[1][0])[:6]: print(k,v)
