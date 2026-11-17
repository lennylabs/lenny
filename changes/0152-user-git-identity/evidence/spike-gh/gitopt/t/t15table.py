# t15table.py <policy-label>: one line per row: reached with a real credential, the forwarded target, and any refusal.
import json,sys
pol=sys.argv[1]
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
import bisect
def window(xs,a,b): return [x for x in xs if a<=x.get('t','')<=b]
rows=[]; tot={}
for s in map(json.loads,open(f'/out/review/{pol}.steps')):
    a,b=s['start'],s['end']
    ref=[x.get('reason','') for x in window(na,a,b) if x.get('decision')=='refused']
    recs=[x for x in window(og,a,b) if 'method' in x]
    real=False; uri=''; fields=[]; hdrs=[]; own=''
    for x in recs:
        h=x.get('headers') or x.get('hdrs') or {}
        for k,v in h.items():
            for y in v:
                if 'REAL' in y or 'MINT-' in y: real=True
                if 'AGENT-OWN' in y: own=' client-credential-at-origin'
        for k in h:
            if k.lower() in('x-original-url','x-rewrite-url','x-http-method-override','sudo'): hdrs.append(k+'='+h[k][0][:18])
        uri=x['method']+' '+x.get('uri','')
        for g in x.get('gql') or []:
            for o in g.get('ops') or []: fields+=o['fields']
    st='REACHED' if recs and real else 'reached-no-cred' if recs else 'refused' if ref or (s['http'] and s['http'][0] in '45') else 'no-answer'
    k=(s['fl'],st); tot[k]=tot.get(k,0)+1
    rows.append(f"{st:15s} {s['fl']:7s} {s['proto']} {s['label'][:44]:44s} http={s['http']:3s} fwd={uri[:66]} {','.join(sorted(set(fields)))[:30]} {' '.join(sorted(set(hdrs)))}{own} | {(ref[0] if ref else s['err'])[:80]}")
print('\n'.join(rows)); print('TOTALS',sorted(tot.items()))
