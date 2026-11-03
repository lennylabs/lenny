import json,glob,os
def load(f): return [json.loads(l) for l in open(f)]
same=0; d=0; ex=None; bad=0
for f in sorted(glob.glob('/o/raw/canon-ghes/*.jsonl')):
    a=load(f); b=load('/o/raw/filtered-ghes/'+os.path.basename(f))
    for x,y in zip(sorted(a,key=lambda r:(r['method'],r['uri'],str([o['fields'] for g in r.get('gql',[]) for o in g.get('ops',[])]))),sorted(b,key=lambda r:(r['method'],r['uri'],str([o['fields'] for g in r.get('gql',[]) for o in g.get('ops',[])])))):
        if not x.get('gql'): continue
        gx,gy=x['gql'][0],y['gql'][0]
        if gx.get('ops')==gy.get('ops'): same+=1
        else: bad+=1
        if gx['doc']!=gy['doc']:
            d+=1
            if ex is None: ex=(gy['doc'][:200],gx['doc'][:200],x['bodyLen'],y['bodyLen'])
print('GraphQL requests with the same operations and fields after canonical printing:',same,'different:',bad,'; documents whose text changed:',d); print(ex)
