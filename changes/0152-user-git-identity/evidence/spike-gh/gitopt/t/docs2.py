import json
old=set(json.load(open('/docs/docs.json'))); new=set()
for l in open('/log/origin.jsonl'):
    try: x=json.loads(l)
    except: continue
    ua=((x.get('headers') or {}).get('User-Agent') or [''])[0]
    if not (ua.startswith('GitHub CLI') or ua.startswith('glab')): continue
    for g in x.get('gql') or []:
        if g.get('doc'): new.add(g['doc'])
json.dump(sorted(new),open('/docs2/new.json','w')); json.dump(sorted(old|new),open('/docs2/all.json','w')); print('earlier',len(old),'this run, as printed',len(new),'union',len(old|new))
