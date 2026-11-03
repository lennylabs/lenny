import json
d=json.load(open('/o/src/ghdocs/fg.json'))
print(type(d), list(d.keys())[:6] if isinstance(d,dict) else len(d))
want=[('post','/repos/{owner}/{repo}/pulls/{pull_number}/reviews'),('post','/repos/{owner}/{repo}/pulls/{pull_number}/reviews/{review_id}/events'),('put','/repos/{owner}/{repo}/pulls/{pull_number}/merge'),('post','/repos/{owner}/{repo}/pulls'),('patch','/repos/{owner}/{repo}/pulls/{pull_number}'),('post','/repos/{owner}/{repo}/merges'),('put','/repos/{owner}/{repo}/pulls/{pull_number}/update-branch'),('patch','/repos/{owner}/{repo}/git/refs/{ref}')]
def walk(o,path=()):
    if isinstance(o,dict):
        if 'permissions' in o and isinstance(o['permissions'],list) and 'title' in o:
            for p in o['permissions']:
                if isinstance(p,dict):
                    for pp in ([p] if 'verb' in p else []):
                        yield (o.get('title'),pp)
        for k,v in o.items():
            yield from walk(v,path+(k,))
    elif isinstance(o,list):
        for v in o: yield from walk(v,path)
seen=set()
for t,p in walk(d):
    k=(p.get('verb'),p.get('requestPath'))
    if k in want and (t,k) not in seen:
        seen.add((t,k))
        print(p.get('verb').upper(),p.get('requestPath'),'->',t,'|',p.get('access'), '| additional:',p.get('additional-permissions'),'| ',json.dumps({a:b for a,b in p.items() if a not in('verb','requestPath','category','subcategory','slug','access')})[:160])
