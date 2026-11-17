# corpus_cmp.py: the filter's verdict on each document against what the other readers see.
import json
Q={"repository","viewer","organization","node","search","__type"}; M={"createPullRequest","updatePullRequest","requestReviews","requestReviewsByLogin","replaceActorsForAssignable","addProjectV2ItemById"}
c=json.load(open('/o/run/corpus.json')); R={}
for n,f in (('go','/o/raw/corpus-go.json'),):
    g=json.load(open(f))
    for i,v in g.items(): R.setdefault(i,{}).update(v)
for n,f in (('graphql-js','/o/raw/corpus-js.json'),('graphql-ruby','/o/raw/corpus-ruby.json'),('graphql-ruby-c','/o/raw/corpus-ruby-c.json')):
    try: g=json.load(open(f))
    except Exception as e: print('missing',n,e); continue
    for i,v in g.items(): R.setdefault(i,{})[n]=v
def s(r):
    if 'err' in r: return 'ERR'
    o=[f"{x['type'][0]}:{','.join(x['fields'])}" for x in r.get('ops',[])]
    if r.get('other'): o.append('other:'+','.join(r['other']))
    return ' | '.join(o) or 'none'
def allowed(r):
    if 'err' in r or not r.get('ops') or r.get('other'): return False
    for x in r['ops']:
        if x['type']=='query' and all(f in Q for f in x['fields']) and x['fields']: continue
        if x['type']=='mutation' and all(f in M for f in x['fields']) and x['fields']: continue
        return False
    return True
readers=['gqlparser','graphql-go','graphql-js','graphql-ruby','graphql-ruby-c']
print("id\t"+"\t".join(readers)+"\tdifference")
findings=[]
for i in sorted(c):
    r=R[i]; row=[s(r.get(n,{'err':'absent'})) if n in r else 'n/a' for n in readers]
    a=allowed(r['gqlparser'])
    d=[]
    for n in readers[1:]:
        if n not in r: continue
        if a and 'err' not in r[n] and not allowed(r[n]): d.append(f"BYPASS-CANDIDATE:{n}")
        elif a and 'err' in r[n]: d.append(f"{n} rejects what the filter allows (fails safe)")
        elif not a and 'err' not in r['gqlparser'] and allowed(r[n]): d.append(f"{n} would allow what the filter refuses")
    print(i+"\t"+"\t".join(x[:60] for x in row)+"\t"+"; ".join(d))
    if any('BYPASS' in x for x in d): findings.append((i,repr(c[i])[:160],d,{n:s(r[n]) for n in r}))
print("\nfilter allows ->",sum(1 for i in c if allowed(R[i]['gqlparser'])),"of",len(c))
print("bypass candidates:",len(findings))
for f in findings: print(f)
