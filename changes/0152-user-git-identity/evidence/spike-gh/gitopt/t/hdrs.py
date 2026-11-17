# hdrs.py <since>: headers by client and every filter refusal in the client-side log after a timestamp.
import json,sys,collections
since=sys.argv[1]; H=collections.defaultdict(collections.Counter); R=collections.Counter(); V=collections.defaultdict(set)
for l in open('/log/na.jsonl'):
    x=json.loads(l)
    if x.get('gitOperation') or x.get('t','')<since or 'headers' not in x: continue
    ua=(x['headers'].get('User-Agent') or ['?'])[0].split('/')[0].split(' ')[0]
    for k,v in x['headers'].items():
        H[ua][k]+=1
        if k in('Time-Zone','Accept-Language','Graphql-Features'): V[k].update(v)
    if x.get('decision')=='refused': R[(ua,x['reason'][:90])]+=1
for u,c in H.items(): print(u,dict(c))
print('values',{k:sorted(v) for k,v in V.items()})
print('refusals',len(R))
for k,v in R.most_common(): print(' ',v,k)
