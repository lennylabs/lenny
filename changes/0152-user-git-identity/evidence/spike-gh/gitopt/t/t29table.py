import json,bisect,sys
f=sys.argv[1] if len(sys.argv)>1 else 'rows'
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
na=[x for x in na if 't' in x]; og=[x for x in og if 't' in x]; na.sort(key=lambda x:x['t']); og.sort(key=lambda x:x['t']); nt=[x['t'] for x in na]; ot=[x['t'] for x in og]
w=lambda xs,ts,a,b: xs[bisect.bisect_left(ts,a):bisect.bisect_right(ts,b)]
tot={}
for s in map(json.loads,open(f'/out/uc5/git/{f}.steps')):
    ref=[x.get('reason','') for x in w(na,nt,s['start'],s['end']) if x.get('decision')=='refused']
    rec=[x for x in w(og,ot,s['start'],s['end'])]
    hd=[]
    for x in rec:
        h=x.get('hdrs') or x.get('headers') or {}
        for k,v in h.items():
            if k.lower() in('authorization','cookie','private-token','x-api-key','x-evil','git-protocol'): hd.append(k+'='+v[0][:34])
    tk=sorted({str(x.get('token') if 'token' in x else x.get('tok','')) for x in rec})
    got='fwd' if rec else 'ref'
    k=(s['exp'],got); tot[k]=tot.get(k,0)+1
    print(f"{'    ' if s['exp']==got else 'DIFF'} exp={s['exp']} got={got} http={s['http']:4s} {s['label'][:62]:62s} {' '.join(sorted(set(hd)))[:90]} | {(ref[0] if ref else s['err'])[:92]}")
print('TOTALS',sorted(tot.items()))
