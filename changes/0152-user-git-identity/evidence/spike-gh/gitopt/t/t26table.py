# t20table.py <label>: one line per row: what the design text predicts, what happened, what the stub saw.
import json,sys
pol=sys.argv[1]
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
na=[x for x in na if 't' in x]; og=[x for x in og if 't' in x]
import bisect
na.sort(key=lambda x:x['t']); og.sort(key=lambda x:x['t']); nt=[x['t'] for x in na]; ot=[x['t'] for x in og]
def window(xs,ts,a,b): return xs[bisect.bisect_left(ts,a):bisect.bisect_right(ts,b)]
rows=[]; tot={}; diffs=[]
for s in map(json.loads,open(f'/out/uc5/{pol}.steps')):
    a,b=s['start'],s['end']
    ref=[x.get('reason','') for x in window(na,nt,a,b) if x.get('decision')=='refused']
    recs=[x for x in window(og,ot,a,b) if 'method' in x and 'headers' in x]
    uri='';fields=[];hd=[];body='';
    for x in recs:
        h=x['headers']; uri=x['method']+' '+x.get('uri','')
        for k,v in h.items():
            lk=k.lower()
            if lk in('authorization','private-token','job-token','deploy-token','x-api-key','cookie','proxy-authorization','sudo','x-forwarded-uri','x-envoy-original-path','x-original-method','time-zone','x-future'): hd.append(k+'='+v[0][:22])
        for g in x.get('gql') or []:
            for o in g.get('ops') or []: fields+=[o['type'][0]+':'+f for f in o['fields']]
        body=x.get('body','')
    got='fwd' if recs else 'ref'
    k=(s['fl'],s['exp'],got); tot[k]=tot.get(k,0)+1
    flag='   ' if s['exp']==got else 'DIFF'
    extra=''
    if 'reencode' in s['label'] or 're-encoded' in s['label']: extra=' stubBody='+body[:170]
    if s['label'].startswith(('hdr','ovr Sudo','gl ovr Sudo')): extra=' '+' '.join(sorted(set(hd)))
    rows.append(f"{flag:4s} exp={s['exp']} got={got} {s['fl']:6s} {s['proto']} http={s['http']:7s} {s['label'][:60]:60s} fwd={uri[:70]} {','.join(sorted(set(fields)))[:40]}{extra} | {(ref[0] if ref else s.get('err',''))[:96]}")
print('\n'.join(rows)); print('TOTALS',sorted(tot.items()))
