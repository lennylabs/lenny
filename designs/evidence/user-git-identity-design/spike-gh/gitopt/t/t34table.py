import json,bisect,sys
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
na=[x for x in na if 't' in x]; og=[x for x in og if 't' in x]; na.sort(key=lambda x:x['t']); og.sort(key=lambda x:x['t']); nt=[x['t'] for x in na]; ot=[x['t'] for x in og]
w=lambda xs,ts,a,b: xs[bisect.bisect_left(ts,a):bisect.bisect_right(ts,b)]
tot={}
for s in map(json.loads,open('/out/uc6/rows.steps')):
    ref=[x.get('reason','') for x in w(na,nt,s['start'],s['end']) if x.get('decision')=='refused']
    rec=[x for x in w(og,ot,s['start'],s['end'])]
    got='fwd' if rec else 'ref'
    k=(s['group'],s['exp'],got); tot[k]=tot.get(k,0)+1
    uri=(rec[0].get('method','')+' '+rec[0].get('uri',''))[:44] if rec else ''
    print(f"{'    ' if s['exp']==got else 'DIFF'} {s['group']} exp={s['exp']} got={got} {s['proto']:3s} http={s['http']:4s} {s['label'][:66]:66s} {uri} | {(ref[0] if ref else s['err'])[:84]}")
print('TOTALS',sorted(tot.items()))
