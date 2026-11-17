import json,glob,os
def load(f): return [json.loads(l) for l in open(f)]
n=0
for f in sorted(glob.glob('/o/raw/canon-ghes/*.jsonl')):
    a=load(f); b=load('/o/raw/filtered-ghes/'+os.path.basename(f))
    ka=lambda r:(r['method'],r['uri'],str((r.get('gql') or [{}])[0].get('ops')))
    A=sorted(a,key=ka); B=sorted(b,key=ka)
    if len(A)!=len(B): print('LEN',f,len(A),len(B))
    for x,y in zip(A,B):
        if not x.get('gql') or not y.get('gql'): continue
        if x['gql'][0].get('ops')!=y['gql'][0].get('ops'):
            n+=1
            if n<=3: print(os.path.basename(f),'canon',x['gql'][0].get('ops'),'| strict',y['gql'][0].get('ops'),'err',x['gql'][0].get('err'))
print('after sorting both sides by op: differences',n)
