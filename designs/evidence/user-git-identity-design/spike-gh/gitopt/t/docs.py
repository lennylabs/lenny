# docs.py: every distinct GraphQL document gh or glab sent in this spike, from the stub logs and the earlier captures.
import json,glob
d=set()
for f in glob.glob('/log/origin.jsonl')+glob.glob('/old/capture-*/*.jsonl')+glob.glob('/out/logs/*.jsonl'):
    for l in open(f):
        try: x=json.loads(l)
        except: continue
        ua=(x.get('headers',{}).get('User-Agent') or [''])[0]
        if not (ua.startswith('GitHub CLI') or ua.startswith('glab')): continue
        for g in x.get('gql') or []:
            if g.get('doc'): d.add(g['doc'])
json.dump(sorted(d),open('/docs/docs.json','w')); print(len(d),'documents; largest',max(map(len,d)))
